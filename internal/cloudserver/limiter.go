package cloudserver

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Tokens are far too long to guess. The limit keeps a broken or hostile client
// from costing work and flooding the log.
const maxTrackedClients = 10_000

type failures struct {
	count int
	since time.Time
}

// limiter counts failed logins per client in fixed windows.
type limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	clients map[string]*failures
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, now: time.Now, clients: map[string]*failures{}}
}

// current returns a client's live window, or nil. The caller holds mu.
func (l *limiter) current(client string) *failures {
	f := l.clients[client]
	if f != nil && l.now().Sub(f.since) >= l.window {
		delete(l.clients, client)
		return nil
	}
	return f
}

func (l *limiter) blocked(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.current(client)
	return f != nil && f.count >= l.max
}

// fail records a failed login and reports whether the client is over the limit.
func (l *limiter) fail(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.current(client)
	if f == nil {
		if len(l.clients) >= maxTrackedClients {
			for name := range l.clients {
				l.current(name)
			}
			if len(l.clients) >= maxTrackedClients {
				client = "" // share one bucket rather than grow without bound
				if f = l.current(client); f == nil {
					f = &failures{since: l.now()}
					l.clients[client] = f
				}
			}
		}
		if f == nil {
			f = &failures{since: l.now()}
			l.clients[client] = f
		}
	}
	f.count++
	if f.count == l.max {
		log.Printf("many failed logins from %q; limiting for %v", client, l.window)
	}
	return f.count > l.max
}

// clientOf names who sent a request, or "" when it cannot be told apart from
// others. Behind a proxy every request comes from the proxy's address, so
// blocking by that address would lock everyone out.
func clientOf(r *http.Request, header string) string {
	var ip net.IP
	if header != "" {
		// The proxy appends the address it saw, so only the last one is its word.
		values := r.Header.Values(header)
		if len(values) == 0 {
			return ""
		}
		parts := strings.Split(values[len(values)-1], ",")
		ip = net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
	} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip = net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return ""
		}
	}
	if ip == nil {
		return ""
	}
	if ip.To4() == nil {
		// One IPv6 user usually holds a whole /64.
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip.String()
}
