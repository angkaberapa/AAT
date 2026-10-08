package limit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func statusOf(h http.Handler, client string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hazards", nil)
	req.Header.Set("X-Client", client)
	h.ServeHTTP(rec, req)
	return rec
}

func headerKey(r *http.Request) (string, bool) {
	k := r.Header.Get("X-Client")
	return k, k != ""
}

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func TestPerClientRejectsAfterBurstWith429(t *testing.T) {
	h := NewPerClient(1, 3, headerKey).Middleware(ok)
	for i := 0; i < 3; i++ {
		if rec := statusOf(h, "media"); rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, rec.Code)
		}
	}
	rec := statusOf(h, "media")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status = %d, Retry-After = %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestPerClientIsolatesClients(t *testing.T) {
	h := NewPerClient(1, 1, headerKey).Middleware(ok)
	statusOf(h, "media")
	if rec := statusOf(h, "media"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("media second request = %d", rec.Code)
	}
	// A noisy media client must not use up the field team's budget.
	if rec := statusOf(h, "field-team"); rec.Code != http.StatusOK {
		t.Fatalf("field-team request = %d", rec.Code)
	}
}

func TestPerClientDisabledAndUnattributable(t *testing.T) {
	disabled := NewPerClient(0, 1, headerKey).Middleware(ok)
	for i := 0; i < 10; i++ {
		if rec := statusOf(disabled, "media"); rec.Code != http.StatusOK {
			t.Fatal("rps=0 should disable the limiter")
		}
	}
	limited := NewPerClient(1, 1, headerKey).Middleware(ok)
	for i := 0; i < 3; i++ {
		if rec := statusOf(limited, ""); rec.Code != http.StatusOK {
			t.Fatal("requests without a client key are not rate limited per client")
		}
	}
}

func TestConcurrencyCapRejectsInsteadOfQueueing(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 10)
	blocking := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := Concurrency(2)(blocking)

	var done sync.WaitGroup
	for i := 0; i < 2; i++ {
		done.Add(1)
		go func() { defer done.Done(); statusOf(h, "x") }()
	}
	<-entered
	<-entered // both slots are now taken

	if rec := statusOf(h, "x"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third concurrent request status = %d, want 429", rec.Code)
	}
	close(release)
	done.Wait()

	if rec := statusOf(h, "x"); rec.Code != http.StatusOK {
		t.Fatalf("after slots freed status = %d", rec.Code)
	}
}

func TestConcurrencyDisabled(t *testing.T) {
	if rec := statusOf(Concurrency(0)(ok), "x"); rec.Code != http.StatusOK {
		t.Fatal("max=0 should disable the cap")
	}
}
