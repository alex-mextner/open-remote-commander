package controlplane

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type ipLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(ratePerSecond float64, burst int) *ipLimiter {
	return &ipLimiter{rate: ratePerSecond, burst: float64(burst), buckets: make(map[string]bucket)}
}

func (l *ipLimiter) allow(r *http.Request, namespace string) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	key := namespace + "\x00" + host
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b.last.IsZero() {
		b.tokens = l.burst
	} else {
		b.tokens += now.Sub(b.last).Seconds() * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.last = now
	if b.tokens < 1 {
		l.buckets[key] = b
		return false
	}
	b.tokens--
	l.buckets[key] = b
	if len(l.buckets) > 4096 {
		cutoff := now.Add(-30 * time.Minute)
		for k, old := range l.buckets {
			if old.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
	}
	return true
}
