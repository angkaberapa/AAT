// Package summarizer turns recent hazard events and anomaly advisories into a
// short situation summary. The LLM is optional: when it is not configured,
// slow, or broken, the caller gets a deterministic template instead.
package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-ai-advisory/internal/detector"
)

// Event is the small slice of a hazard event needed for a situation summary.
type Event struct {
	Source     string
	HazardType string
	Severity   string
	AreaName   string
	OccurredAt time.Time
}

type Config struct {
	BaseURL   string
	APIKey    string
	Model     string
	Thinking  string
	Timeout   time.Duration
	MaxTokens int
}

type Result struct {
	Summary     string    `json:"summary"`
	Source      string    `json:"source"`
	Model       string    `json:"model,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
}

type Summarizer struct {
	cfg    Config
	client *http.Client
	logger *slog.Logger
	now    func() time.Time
}

func New(cfg Config, logger *slog.Logger) *Summarizer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 200
	}
	return &Summarizer{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		logger: logger,
		now:    time.Now,
	}
}

// Summarize never returns an error: an unavailable LLM must not turn into a
// failed request on the caller side. The Source field tells the truth.
func (s *Summarizer) Summarize(ctx context.Context, events []Event, advisories []detector.Advisory) Result {
	fallback := Result{
		Summary:     Fallback(events, advisories),
		Source:      "fallback",
		GeneratedAt: s.now().UTC(),
	}
	if s.cfg.BaseURL == "" || s.cfg.Model == "" || s.cfg.APIKey == "" {
		fallback.Reason = "llm_not_configured"
		return fallback
	}

	summary, err := s.call(ctx, events, advisories)
	if err != nil {
		s.logger.WarnContext(ctx, "llm summary failed, using fallback", "error", err.Error())
		fallback.Reason = "llm_error"
		return fallback
	}
	return Result{
		Summary:     summary,
		Source:      "llm",
		Model:       s.cfg.Model,
		GeneratedAt: s.now().UTC(),
	}
}

func (s *Summarizer) call(ctx context.Context, events []Event, advisories []detector.Advisory) (string, error) {
	request := map[string]any{
		"model":       s.cfg.Model,
		"temperature": 0.2,
		"max_tokens":  s.cfg.MaxTokens,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "Anda asisten situasi BNPB. Ringkas hanya berdasarkan data yang diberikan. " +
					"Jangan menambah fakta. Bahasa Indonesia, maksimal tiga kalimat.",
			},
			{"role": "user", "content": prompt(events, advisories)},
		},
	}
	if s.cfg.Thinking != "" {
		request["thinking"] = map[string]string{"type": s.cfg.Thinking}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(s.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if len(body.Choices) == 0 || strings.TrimSpace(body.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("llm returned an empty summary")
	}
	return strings.TrimSpace(body.Choices[0].Message.Content), nil
}

func prompt(events []Event, advisories []detector.Advisory) string {
	var b strings.Builder
	b.WriteString("Event terbaru:\n")
	if len(events) == 0 {
		b.WriteString("- tidak ada event\n")
	}
	for _, event := range events {
		fmt.Fprintf(&b, "- %s %s di %s, severity %s\n",
			event.Source, event.HazardType, event.AreaName, event.Severity)
	}
	b.WriteString("\nAdvisory anomali:\n")
	if len(advisories) == 0 {
		b.WriteString("- tidak ada advisory\n")
	}
	for _, item := range advisories {
		fmt.Fprintf(&b, "- %s %s di %s, level %s, skor %.2f\n",
			item.Source, item.HazardType, item.AreaName, item.Level, item.Score)
	}
	return b.String()
}

// Fallback is the deterministic summary used when the LLM is unavailable.
func Fallback(events []Event, advisories []detector.Advisory) string {
	if len(advisories) > 0 {
		first := advisories[0]
		return fmt.Sprintf(
			"Terdapat %d advisory anomali. Yang terbaru: %s %s di %s dengan level %s dan skor %.2f. "+
				"Data ini hanya sinyal tambahan dan tidak mengubah jalur peringatan.",
			len(advisories), first.Source, first.HazardType, first.AreaName, first.Level, first.Score)
	}
	if len(events) == 0 {
		return "Belum ada event terbaru. Sistem tetap berjalan dan jalur peringatan tidak terpengaruh."
	}
	areas := make(map[string]int)
	for _, event := range events {
		areas[event.AreaName]++
	}
	topArea, topCount := "", 0
	for area, count := range areas {
		if count > topCount {
			topArea, topCount = area, count
		}
	}
	return fmt.Sprintf(
		"Tidak ada advisory anomali. Dari %d event terbaru, wilayah paling sering muncul adalah %s (%d event). "+
			"Ringkasan ini dibuat tanpa LLM.",
		len(events), topArea, topCount)
}
