package agent

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// ipRateLimiter enforces a per-IP token-bucket rate limit with a global ceiling.
// A zero rps value disables limiting entirely.
type ipRateLimiter struct {
	mu     sync.Mutex
	ips    map[string]*ipEntry
	global *rate.Limiter
	rps    rate.Limit
	burst  int
}

func newIPRateLimiter(rps, burst int) *ipRateLimiter {
	if rps == 0 {
		return &ipRateLimiter{}
	}
	return &ipRateLimiter{
		ips:    make(map[string]*ipEntry),
		global: rate.NewLimiter(rate.Limit(rps*10), burst*10),
		rps:    rate.Limit(rps),
		burst:  burst,
	}
}

func (rl *ipRateLimiter) disabled() bool { return rl == nil || rl.rps == 0 }

// allow returns true when ip is within its rate limit.
func (rl *ipRateLimiter) allow(ip string) bool {
	if rl.disabled() {
		return true
	}
	if !rl.global.Allow() {
		return false
	}
	rl.mu.Lock()
	e, ok := rl.ips[ip]
	if !ok {
		e = &ipEntry{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.ips[ip] = e
	}
	e.lastSeen = time.Now()
	allowed := e.limiter.Allow()
	rl.mu.Unlock()
	return allowed
}

// evict removes IP entries idle for more than 5 minutes.
func (rl *ipRateLimiter) evict() {
	if rl.disabled() {
		return
	}
	cutoff := time.Now().Add(-5 * time.Minute)
	rl.mu.Lock()
	for ip, e := range rl.ips {
		if e.lastSeen.Before(cutoff) {
			delete(rl.ips, ip)
		}
	}
	rl.mu.Unlock()
}
