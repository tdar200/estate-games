package server

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	rateWindow = 60 * time.Second
	// maxBuckets bounds the limiter's memory. Keys are per client address
	// (IPv6 per /64), so only a very large botnet gets near it; past it new
	// keys are refused until the next prune.
	maxBuckets = 50_000
)

// rateLimiter is a fixed-window limiter (consumeRateLimit in server.js).
// Expired buckets are dropped by prune, which the server's sweep calls every
// few seconds, never by a full scan on the request path.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*rateBucket
	lastPrune time.Time
}

type rateBucket struct {
	startedAt time.Time
	count     int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]*rateBucket{}}
}

func (l *rateLimiter) consume(key string, limit int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.buckets[key]
	if current == nil || now.Sub(current.startedAt) >= rateWindow {
		if current == nil && len(l.buckets) >= maxBuckets {
			// At most one inline prune per second, so a flood of new keys
			// can't turn every request into a full scan.
			if now.Sub(l.lastPrune) >= time.Second {
				l.pruneLocked(now)
			}
			if len(l.buckets) >= maxBuckets {
				return false
			}
		}
		l.buckets[key] = &rateBucket{startedAt: now, count: 1}
		return true
	}
	current.count++
	return current.count <= limit
}

// prune drops buckets whose window has ended.
func (l *rateLimiter) prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
}

func (l *rateLimiter) pruneLocked(now time.Time) {
	l.lastPrune = now
	for key, bucket := range l.buckets {
		if now.Sub(bucket.startedAt) >= rateWindow {
			delete(l.buckets, key)
		}
	}
}

func (l *rateLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

var (
	reStatePath = regexp.MustCompile(`/state$`)
	reEntryPath = regexp.MustCompile(`/(join|watch)$`)
)

const msgTooManyRequests = "Too many requests; please try again shortly"

// apiRateKey classifies an /api/ request: state reads (polling clients; 600
// per minute per address, enough for several phones polling twice a second
// behind one router), entries (30 per minute: create, join, watch, tables,
// device, rejoin, reclaim, resume) and everything else (actionRateKey).
// Keys are per client address only: keying on anything the client sends
// (Node keyed state reads on ?token=) lets random values mint fresh buckets.
func apiRateKey(r *http.Request) (key string, limit int) {
	path := r.URL.Path
	isStateRead := r.Method == http.MethodGet && reStatePath.MatchString(path)
	isEntry := r.Method == http.MethodPost && (path == "/api/rooms" || reEntryPath.MatchString(path) ||
		path == "/api/tables" || path == "/api/games" || path == "/api/device" || strings.HasSuffix(path, "/rejoin") ||
		strings.HasSuffix(path, "/reclaim") || strings.HasSuffix(path, "/resume"))
	switch {
	case isStateRead:
		return addressKey(clientIP(r)) + ":state", 600
	case isEntry:
		return addressKey(clientIP(r)) + ":entry", 30
	}
	return actionRateKey(clientIP(r))
}

// actionRateKey is the tier of everything that is not a state read or an
// entry: 180 per minute per address. Actions sent over a room socket count
// here too, in the same bucket as POST …/action from that address.
func actionRateKey(ip string) (key string, limit int) {
	return addressKey(ip) + ":action", 180
}

// addressKey groups IPv6 clients by /64 (one home or phone gets a whole
// /64, so per-address limits would otherwise be trivial to dodge).
func addressKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() != nil {
		return ip
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// clientIP is the request's client address. X-Forwarded-For is trusted only
// from a local proxy (Tailscale Funnel or serve forward from 127.0.0.1), and
// only its last entry: the one that proxy itself appended. Earlier entries
// are whatever the client claimed.
func clientIP(r *http.Request) string {
	direct := directIP(r)
	if !isLoopback(direct) {
		return direct
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if strings.TrimSpace(forwarded) == "" {
		return direct
	}
	parts := strings.Split(forwarded, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if host, _, err := net.SplitHostPort(last); err == nil {
		last = host
	}
	if net.ParseIP(last) == nil {
		return direct
	}
	return last
}

func directIP(r *http.Request) string {
	direct := r.RemoteAddr
	if host, _, err := net.SplitHostPort(direct); err == nil {
		direct = host
	}
	if direct == "" {
		direct = "unknown"
	}
	return direct
}

func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// localRequest reports a request made on the server machine itself (not
// forwarded by a proxy): the PC table screen on the host, for instance.
// Per-address quotas don't apply to it.
func localRequest(r *http.Request) bool {
	return isLoopback(directIP(r)) && strings.TrimSpace(strings.Join(r.Header.Values("X-Forwarded-For"), "")) == ""
}

// privateAddress reports a client on the local network or the tailnet
// (loopback, RFC 1918, link-local, Tailscale's 100.64.0.0/10, IPv6 ULA).
func privateAddress(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast() {
		return true
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(parsed)
}
