package limit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func roleFromHeader(r *http.Request) string {
	return r.Header.Get("X-Role")
}

func TestPriorityCapsMediaAndReservesField(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Block") == "1" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})
	priority := NewPriority(4, 1, roleFromHeader)
	server := httptest.NewServer(priority.Middleware(handler))
	defer server.Close()

	first := make(chan int, 1)
	go func() {
		request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
		request.Header.Set("X-Role", "media")
		request.Header.Set("X-Block", "1")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			first <- 0
			return
		}
		first <- response.StatusCode
		_ = response.Body.Close()
	}()
	<-entered

	media, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	media.Header.Set("X-Role", "media")
	mediaResponse, err := http.DefaultClient.Do(media)
	if err != nil {
		t.Fatalf("media request: %v", err)
	}
	if mediaResponse.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second media status = %d, want 429", mediaResponse.StatusCode)
	}
	if got := mediaResponse.Header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q", got)
	}
	_ = mediaResponse.Body.Close()

	field, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	field.Header.Set("X-Role", "field")
	fieldResponse, err := http.DefaultClient.Do(field)
	if err != nil {
		t.Fatalf("field request: %v", err)
	}
	if fieldResponse.StatusCode != http.StatusOK {
		t.Fatalf("field status = %d, want 200", fieldResponse.StatusCode)
	}
	_ = fieldResponse.Body.Close()

	close(release)
	if got := <-first; got != http.StatusOK {
		t.Fatalf("first media status = %d", got)
	}
}

func TestPriorityTotalCap(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	priority := NewPriority(1, 1, roleFromHeader)
	server := httptest.NewServer(priority.Middleware(handler))
	defer server.Close()

	go func() {
		request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
		request.Header.Set("X-Role", "field")
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-entered

	second, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	second.Header.Set("X-Role", "field")
	response, err := http.DefaultClient.Do(second)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.StatusCode)
	}
	_ = response.Body.Close()
	close(release)
}

func TestPriorityTreatsMissingRoleAsMedia(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	priority := NewPriority(2, 1, roleFromHeader)
	server := httptest.NewServer(priority.Middleware(handler))
	defer server.Close()

	go func() {
		response, err := http.Get(server.URL)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-entered

	second, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	response, err := http.DefaultClient.Do(second)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 for missing role", response.StatusCode)
	}
	_ = response.Body.Close()
	close(release)
}
