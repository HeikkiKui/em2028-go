package main

import (
	"math"
	"sync"
	"time"
)

// Muistissa toimiva token bucket. IP-osoitteita ei tallenneta levylle eikä lokiin.
type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu    sync.Mutex
	m     map[string]*bucket
	rate  float64 // täyttyminen / sekunti
	burst float64
}

func newLimiter(perSecond float64, burst int) *limiter {
	return &limiter{m: make(map[string]*bucket), rate: perSecond, burst: float64(burst)}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.m) > 50000 { // suoja muistin täyttämiseltä
		l.m = make(map[string]*bucket)
	}
	b, ok := l.m[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for k, b := range l.m {
		if b.last.Before(cutoff) {
			delete(l.m, k)
		}
	}
}
