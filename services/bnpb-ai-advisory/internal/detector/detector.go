// Package detector implements an online anomaly detector for hazard events.
//
// The model is deliberately small and explainable: for every
// (source, hazard_type, area) key it learns the mean and variance of the event
// rate and severity with Welford's algorithm, then scores a new event with a
// diagonal Gaussian z-score. A high score means the event arrives much faster,
// or with a much higher severity, than the learned baseline.
package detector

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Hazard is the subset of the canonical event that the detector needs.
type Hazard struct {
	HazardID   string
	Source     string
	HazardType string
	Severity   string
	AreaName   string
	OccurredAt time.Time
}

// Advisory is an anomaly signal. It is advisory only: it never changes a
// HazardEvent and is never written to the canonical store.
type Advisory struct {
	ID                    string    `json:"id"`
	CreatedAt             time.Time `json:"created_at"`
	Source                string    `json:"source"`
	HazardType            string    `json:"hazard_type"`
	AreaName              string    `json:"area_name"`
	Level                 string    `json:"level"`
	Score                 float64   `json:"score"`
	Threshold             float64   `json:"threshold"`
	Method                string    `json:"method"`
	Reason                string    `json:"reason"`
	ObservedRatePerMinute float64   `json:"observed_rate_per_minute"`
	BaselineRatePerMinute float64   `json:"baseline_rate_per_minute"`
	BaselineSamples       int       `json:"baseline_samples"`
}

// Config controls the detector. Zero values are replaced by demo-friendly
// defaults.
type Config struct {
	Threshold  float64
	MinSamples int
	Cooldown   time.Duration
	Now        func() time.Time
}

type model struct {
	rateN    int
	meanRate float64
	m2Rate   float64

	sevN    int
	meanSev float64
	m2Sev   float64

	lastSeen     time.Time
	lastAdvisory time.Time
}

// Detector is safe for concurrent use.
type Detector struct {
	mu     sync.Mutex
	models map[string]*model
	cfg    Config
	seq    atomic.Uint64
}

func New(cfg Config) *Detector {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 3.5
	}
	if cfg.MinSamples < 2 {
		cfg.MinSamples = 20
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Detector{
		models: make(map[string]*model),
		cfg:    cfg,
	}
}

// Observe updates the baseline with h and returns an advisory when the event is
// anomalous. The baseline is not updated with anomalous samples, so a burst
// cannot silently become the new normal.
func (d *Detector) Observe(h Hazard) (Advisory, bool) {
	now := d.cfg.Now().UTC()
	eventTime := h.OccurredAt.UTC()
	if eventTime.IsZero() {
		eventTime = now
	}
	key := h.Source + "|" + h.HazardType + "|" + h.AreaName
	sev := severityValue(h.Severity)

	d.mu.Lock()
	defer d.mu.Unlock()

	m := d.models[key]
	if m == nil {
		m = &model{lastSeen: eventTime}
		m.addSeverity(sev)
		d.models[key] = m
		return Advisory{}, false
	}

	delta := eventTime.Sub(m.lastSeen).Seconds()
	if delta <= 0 {
		delta = 0.001
	}
	m.lastSeen = eventTime

	rate := 60.0 / delta
	if rate > 60000 {
		rate = 60000
	}

	score, reason := m.score(rate, sev, d.cfg.MinSamples)
	anomalous := score >= d.cfg.Threshold && rate > m.meanRate
	if !anomalous {
		m.addRate(rate)
		m.addSeverity(sev)
		return Advisory{}, false
	}

	if !m.lastAdvisory.IsZero() && now.Sub(m.lastAdvisory) < d.cfg.Cooldown {
		return Advisory{}, false
	}
	m.lastAdvisory = now

	level := "warning"
	if score >= d.cfg.Threshold*1.5 {
		level = "critical"
	}
	// Cap the score so a very sharp burst stays readable; crossing the
	// threshold is what matters, not the raw magnitude.
	if score > 100 {
		score = 100
	}

	return Advisory{
		ID:                    fmt.Sprintf("ADV-%d-%d", now.UnixNano(), d.seq.Add(1)),
		CreatedAt:             now,
		Source:                h.Source,
		HazardType:            h.HazardType,
		AreaName:              h.AreaName,
		Level:                 level,
		Score:                 round(score),
		Threshold:             d.cfg.Threshold,
		Method:                "online_gaussian",
		Reason:                reason,
		ObservedRatePerMinute: round(rate),
		BaselineRatePerMinute: round(m.meanRate),
		BaselineSamples:       m.rateN,
	}, true
}

func (m *model) score(rate, sev float64, minSamples int) (float64, string) {
	if m.rateN < minSamples {
		return 0, ""
	}

	rateStd := safeStd(m.m2Rate, m.rateN, 1.0)
	sevStd := safeStd(m.m2Sev, m.sevN, 0.25)

	zRate := (rate - m.meanRate) / rateStd
	zSev := 0.0
	if sevStd > 0 {
		zSev = (sev - m.meanSev) / sevStd
	}

	score := math.Hypot(zRate, zSev)
	reason := "event rate spike"
	if math.Abs(zSev) > math.Abs(zRate) {
		reason = "severity spike"
	}
	return score, reason
}

func (m *model) addRate(value float64) {
	m.rateN++
	delta := value - m.meanRate
	m.meanRate += delta / float64(m.rateN)
	m.m2Rate += delta * (value - m.meanRate)
}

func (m *model) addSeverity(value float64) {
	m.sevN++
	delta := value - m.meanSev
	m.meanSev += delta / float64(m.sevN)
	m.m2Sev += delta * (value - m.meanSev)
}

func safeStd(m2 float64, n int, floor float64) float64 {
	if n < 2 || m2 <= 0 {
		return floor
	}
	std := math.Sqrt(m2 / float64(n-1))
	if std < floor {
		return floor
	}
	return std
}

func severityValue(severity string) float64 {
	switch severity {
	case "AWAS":
		return 3
	case "SIAGA":
		return 2
	case "WASPADA":
		return 1
	default:
		return 0
	}
}

func round(value float64) float64 {
	return math.Round(value*100) / 100
}
