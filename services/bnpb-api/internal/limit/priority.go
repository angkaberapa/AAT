package limit

import (
	"net/http"
	"sync"
	"time"
)

// Role returns the authenticated role for a request. Empty means the token
// predates the role claim, so it is treated as media (the stricter class).
type Role func(*http.Request) string

// Priority reserves capacity for field and ops traffic while capping media.
// MediaMax must stay below TotalMax, otherwise media can occupy every slot.
type Priority struct {
	mu       sync.Mutex
	totalMax int
	mediaMax int
	total    int
	media    int
	role     Role
}

func NewPriority(totalMax, mediaMax int, role Role) *Priority {
	if mediaMax < 0 {
		mediaMax = 0
	}
	if totalMax > 0 && mediaMax > totalMax {
		mediaMax = totalMax
	}
	return &Priority{totalMax: totalMax, mediaMax: mediaMax, role: role}
}

func (p *Priority) Middleware(next http.Handler) http.Handler {
	if p.totalMax <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := ""
		if p.role != nil {
			role = p.role(r)
		}
		isMedia := role == "" || role == "media"

		p.mu.Lock()
		if p.total >= p.totalMax || (isMedia && p.mediaMax > 0 && p.media >= p.mediaMax) {
			p.mu.Unlock()
			reject(w, time.Second, "server is at its capacity")
			return
		}
		p.total++
		if isMedia {
			p.media++
		}
		p.mu.Unlock()

		defer func() {
			p.mu.Lock()
			p.total--
			if isMedia {
				p.media--
			}
			p.mu.Unlock()
		}()

		next.ServeHTTP(w, r)
	})
}
