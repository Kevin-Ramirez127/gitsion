package github

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Usage is process-local request telemetry. GitHub's remaining quota may also
// reflect requests made by other applications using the same user's budget.
type Usage struct {
	Requests       uint64
	RateLimitKnown bool
	Limit          int
	Remaining      int
	ResetAt        time.Time
}

type requestBudget struct {
	mu     sync.Mutex
	usage  Usage
	paused time.Time
}

// Usage returns a concurrency-safe snapshot without making an API request.
func (c *Client) Usage() Usage {
	c.quota.mu.Lock()
	defer c.quota.mu.Unlock()
	return c.quota.usage
}

func (b *requestBudget) begin() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.Before(b.paused) {
		return &RateLimitError{ResetAt: b.paused}
	}
	if b.usage.RateLimitKnown && now.Before(b.usage.ResetAt) {
		if b.usage.Remaining <= 0 {
			return &RateLimitError{ResetAt: b.usage.ResetAt}
		}
		// Reserve quota before concurrent requests start. Out-of-order responses
		// must never increase the remaining budget in the same reset window.
		b.usage.Remaining--
	}
	b.usage.Requests++
	return nil
}

func (b *requestBudget) observe(header http.Header) {
	remaining, err := strconv.Atoi(header.Get("X-RateLimit-Remaining"))
	reset := rateLimitReset(header)
	if err != nil || remaining < 0 || reset.IsZero() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.usage.RateLimitKnown || reset.After(b.usage.ResetAt) ||
		(reset.Equal(b.usage.ResetAt) && remaining < b.usage.Remaining) {
		b.usage.RateLimitKnown = true
		b.usage.Remaining = remaining
		b.usage.ResetAt = reset
	}
	if limit, err := strconv.Atoi(header.Get("X-RateLimit-Limit")); err == nil && limit > 0 {
		b.usage.Limit = limit
	}
}

func (b *requestBudget) pause(header http.Header) error {
	now := time.Now()
	until := now.Add(time.Minute)
	if seconds, err := strconv.Atoi(header.Get("Retry-After")); err == nil && seconds >= 0 {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(header.Get("Retry-After")); err == nil && date.After(now) {
		until = date
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if reset := rateLimitReset(header); reset.After(until) {
			until = reset
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if until.After(b.paused) {
		b.paused = until
	}
	return &RateLimitError{ResetAt: b.paused}
}
