package admin

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

const (
	// This is a conservative provider pacing policy, not an Atlassian quota.
	// Apply it across the client's API surfaces because quota scopes are not
	// fully documented. Separate provider clients/processes do not share it.
	defaultRequestInterval = 200 * time.Millisecond
	maxRateLimitWait       = 5 * time.Minute
)

var errRateLimitWait = errors.New("admin API rate limit wait exceeded")

// rateLimitedTransport gates every network attempt, including retries and
// generated-client requests. It does not hold its mutex during network I/O.
type rateLimitedTransport struct {
	base           http.RoundTripper
	mu             sync.Mutex
	next           time.Time
	blockedUntil   time.Time
	interval       time.Duration
	intervalUntil  time.Time
	consecutive429 int
}

func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.wait(req.Context()); err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, fmt.Errorf("send Admin API request: %w", err)
	}
	if resp != nil {
		t.observe(resp, time.Now())
	}
	return resp, nil
}

func (t *rateLimitedTransport) wait(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, maxRateLimitWait)
	defer cancel()
	for {
		if err := waitCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("wait for Admin API rate limit: %w", ctx.Err())
			}
			return fmt.Errorf("%w: %s", errRateLimitWait, maxRateLimitWait)
		}
		t.mu.Lock()
		now := time.Now()
		until := t.next
		if t.blockedUntil.After(until) {
			until = t.blockedUntil
		}
		if !until.After(now) {
			interval := defaultRequestInterval
			if t.intervalUntil.After(now) && t.interval > interval {
				// The server-derived interval expires with its quota window.
				// Keep the local minimum spacing even when reset is imminent;
				// blockedUntil independently preserves server cooldowns.
				interval = max(interval, min(t.interval, t.intervalUntil.Sub(now)))
			}
			t.next = now.Add(interval)
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()
		timer := time.NewTimer(until.Sub(now))
		select {
		case <-waitCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
		// Recheck the shared deadline: another response may have extended it.
		// Reserve only at dispatch, so cancelled waiters do not consume slots.
	}
}

func (t *rateLimitedTransport) observe(resp *http.Response, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	reset, resetErr := time.Parse(time.RFC3339Nano, resp.Header.Get("X-RateLimit-Reset"))
	remaining, remainingErr := strconv.ParseInt(resp.Header.Get("X-RateLimit-Remaining"), 10, 64)
	if resetErr == nil && reset.After(now) && remainingErr == nil && remaining >= 0 {
		if remaining == 0 {
			t.blockUntil(reset)
		} else {
			interval := reset.Sub(now) / time.Duration(remaining)
			// Out-of-order responses must not accelerate an active window.
			if !t.intervalUntil.After(now) || interval > t.interval {
				t.interval = interval
				t.intervalUntil = reset
			}
			if interval > defaultRequestInterval && now.Add(interval).After(t.next) {
				t.next = now.Add(interval)
			}
		}
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.consecutive429 = 0
		}
		return
	}
	if t.consecutive429 < 5 {
		t.consecutive429++
	}
	if deadline, ok := retryAfterDeadline(resp.Header.Get("Retry-After"), now); ok {
		t.blockUntil(deadline)
		return
	}
	if resetErr == nil && reset.After(now) {
		t.blockUntil(reset)
		return
	}
	// Bounded exponential backoff with jitter when the server gives no usable
	// deadline. The deadline is shared even when this was the final retry.
	delay := time.Second << (t.consecutive429 - 1)
	delay += time.Duration(rand.Int64N(int64(delay))) //nolint:gosec // Retry jitter does not require cryptographic randomness.
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	t.blockUntil(now.Add(delay))
}

func (t *rateLimitedTransport) blockUntil(deadline time.Time) {
	if deadline.After(t.blockedUntil) {
		t.blockedUntil = deadline
	}
}

func retryAfterDeadline(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		// Avoid overflow without shortening a server-requested wait to a retry.
		if seconds > int64((1<<63-1)/time.Second) {
			return now.Add(time.Duration(1<<63 - 1)), true
		}
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	deadline, err := http.ParseTime(value)
	return deadline, err == nil && !deadline.Before(now)
}

func sharedRateLimitBackoff(minimum, maximum time.Duration, attempt int, resp *http.Response) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		// The transport already published the deadline for all callers. Waiting
		// there avoids independent retry timers and handles X-RateLimit-Reset.
		return 0
	}
	return retryablehttp.DefaultBackoff(minimum, maximum, attempt, resp)
}
