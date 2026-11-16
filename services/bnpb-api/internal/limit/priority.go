package limit

import (
	"net/http"
	"sync"
	"time"
)

// Role returns the authenticated role for a request. Empty means the token
// predates the role claim, so it is treated as media (the stricter class).
type Role func(*http.Request) string

// Priority caps media in-flight requests. The total cap stays with Concurrency,
// which already wraps this middleware.
type Priority struct {
	mu       sync.Mutex
	mediaMax int
	media    int
	role     Role
}

func NewPriority(mediaMax int, role Role) *Priority {
	if mediaMax < 0 {
		mediaMax = 0
	}
	return &Priority{mediaMax: mediaMax, role: role}
}

func (p *Priority) Middleware(next http.Handler) http.Handler {
	if p.mediaMax <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := ""
		if p.role != nil {
			role = p.role(r)
		}
		isMedia := role == "" || role == "media"

		if isMedia {
			p.mu.Lock()
			if p.media >= p.mediaMax {
				p.mu.Unlock()
				reject(w, time.Second, "server is at its media capacity")
				return
			}
			p.media++
			p.mu.Unlock()
			defer func() {
				p.mu.Lock()
				p.media--
				p.mu.Unlock()
			}()
		}

		next.ServeHTTP(w, r)
	})
}
