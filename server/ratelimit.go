package main

import (
	"sync"
	"time"
)

// TokenBucket: einfacher Ratenbegrenzer, nicht threadsicher (pro Session benutzt).
type TokenBucket struct {
	tokens float64
	max    float64
	rate   float64 // Tokens pro Sekunde
	last   time.Time
}

func NewTokenBucket(max, perSecond float64) *TokenBucket {
	return &TokenBucket{tokens: max, max: max, rate: perSecond, last: time.Now()}
}

func (b *TokenBucket) Allow() bool {
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.max {
		b.tokens = b.max
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// IPLimiter begrenzt fehlgeschlagene Anmeldeversuche pro IP (gegen Erraten von Einladungscodes).
type IPLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	entries map[string][]time.Time
}

func NewIPLimiter(max int, window time.Duration) *IPLimiter {
	return &IPLimiter{window: window, max: max, entries: map[string][]time.Time{}}
}

func (l *IPLimiter) prune(ip string, now time.Time) []time.Time {
	ts := l.entries[ip]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > l.window {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.entries, ip)
	} else {
		l.entries[ip] = ts
	}
	return ts
}

// Reserve prüft die Sperre und zählt den Versuch in einem Schritt vorab als
// Fehlversuch (sonst könnten parallele Anmeldungen die Grenze umgehen).
// Erfolgreiche Versuche gibt Refund wieder frei.
func (l *IPLimiter) Reserve(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.cleanup(now)
	ts := l.prune(ip, now)
	if len(ts) >= l.max {
		return false
	}
	l.entries[ip] = append(ts, now)
	return true
}

// Refund nimmt einen reservierten Versuch zurück.
func (l *IPLimiter) Refund(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts := l.entries[ip]; len(ts) > 0 {
		if len(ts) == 1 {
			delete(l.entries, ip)
		} else {
			l.entries[ip] = ts[:len(ts)-1]
		}
	}
}

func (l *IPLimiter) cleanup(now time.Time) {
	if len(l.entries) > 1000 { // gelegentlich alles aufräumen
		for k := range l.entries {
			l.prune(k, now)
		}
	}
}
