package fieldreports

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/store"
)

type fakeFieldStore struct{ accepted store.FieldReport }

func (fake *fakeFieldStore) AcceptFieldReport(_ context.Context, report store.FieldReport) (store.FieldReport, bool, error) {
	report.Status = store.FieldReportPending
	fake.accepted = report
	return report, true, nil
}
func (*fakeFieldStore) ClaimFieldReport(context.Context, string, time.Duration) (*store.FieldReport, error) {
	return nil, nil
}
func (*fakeFieldStore) CompleteFieldReport(context.Context, string, string) error { return nil }
func (*fakeFieldStore) GetFieldReport(context.Context, string) (*store.FieldReport, error) {
	return nil, nil
}

func TestFieldReportIsDurablyAcceptedBefore202(t *testing.T) {
	fake := &fakeFieldStore{}
	mux := http.NewServeMux()
	Register(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), fake, "ingest-secret", "audit-secret")
	body := `{"occurred_at":"2026-11-16T08:00:00Z","area_name":"Sleman","latitude":-7.65,"longitude":110.42,"severity":"SIAGA","description":"ash observed"}`
	request := httptest.NewRequest(http.MethodPost, "/internal/field-reports", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	request.Header.Set("Idempotency-Key", "report-1")
	request.Header.Set("X-Client-ID", "field-team")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.accepted.ReportID == "" || fake.accepted.IdempotencyKey != "report-1" || fake.accepted.ClientID != "field-team" {
		t.Fatalf("accepted report = %+v", fake.accepted)
	}
}

func TestFieldReportRejectsWrongInternalCredential(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeFieldStore{}, "ingest-secret", "audit-secret")
	request := httptest.NewRequest(http.MethodPost, "/internal/field-reports", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer wrong")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}
