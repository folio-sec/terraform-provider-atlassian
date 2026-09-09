package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestRateLimitPacesConcurrentRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var sent []time.Time
		transport := &rateLimitedTransport{base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			sent = append(sent, time.Now())
			mu.Unlock()
			return response(r, 204, ""), nil
		})}
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
				resp, err := transport.RoundTrip(req)
				if err != nil {
					t.Error(err)
					return
				}
				_ = resp.Body.Close()
			})
		}
		wg.Wait()
		sort.Slice(sent, func(i, j int) bool { return sent[i].Before(sent[j]) })
		for i := 1; i < len(sent); i++ {
			if gap := sent[i].Sub(sent[i-1]); gap < defaultRequestInterval {
				t.Errorf("dispatch gap = %s", gap)
			}
		}
	})
}

func TestRateLimitExtendsAlreadyWaitingRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &rateLimitedTransport{}
		if err := limiter.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- limiter.wait(context.Background()) }()
		synctest.Wait()
		resp := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"10"}}}
		limiter.observe(resp, time.Now())
		// A shorter, later response cannot clear or shorten the shared pause.
		limiter.observe(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"1"}}}, time.Now())
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Fatalf("waited %s", elapsed)
		}
	})
}

func TestRateLimitCancellationDoesNotReserveSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &rateLimitedTransport{}
		if err := limiter.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- limiter.wait(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
		start := time.Now()
		if err := limiter.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != defaultRequestInterval {
			t.Fatalf("waited %s", elapsed)
		}
	})
}

func TestRateLimitHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		headers http.Header
		delay   time.Duration
	}{
		{"remaining budget", 200, http.Header{"X-Ratelimit-Remaining": {"2"}, "X-Ratelimit-Reset": {"2000-01-01T00:00:10Z"}}, 5 * time.Second},
		{"exhausted budget", 200, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"2000-01-01T00:00:10Z"}}, 10 * time.Second},
		{"429 reset", 429, http.Header{"X-Ratelimit-Reset": {"2000-01-01T00:00:10Z"}}, 10 * time.Second},
		{"http date", 429, http.Header{"Retry-After": {"Sat, 01 Jan 2000 00:00:10 GMT"}}, 10 * time.Second},
		{"invalid headers", 200, http.Header{"X-Ratelimit-Remaining": {"-1"}, "X-Ratelimit-Reset": {"invalid"}}, defaultRequestInterval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limiter := &rateLimitedTransport{}
				if err := limiter.wait(context.Background()); err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				limiter.observe(&http.Response{StatusCode: tc.status, Header: tc.headers}, start)
				if err := limiter.wait(context.Background()); err != nil {
					t.Fatal(err)
				}
				if got := time.Since(start); got != tc.delay {
					t.Fatalf("wait = %s, want %s", got, tc.delay)
				}
			})
		})
	}
}

func TestRateLimitFallbackAndWaitBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &rateLimitedTransport{}
		now := time.Now()
		for i := range 8 {
			limiter.observe(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"invalid"}}}, now)
			delay := limiter.blockedUntil.Sub(now)
			minimum := time.Second << min(i, 4)
			if delay < minimum || delay > 30*time.Second {
				t.Fatalf("fallback %d = %s", i, delay)
			}
		}
		limiter.observe(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"3600"}}}, now)
		if err := limiter.wait(context.Background()); !errors.Is(err, errRateLimitWait) {
			t.Fatalf("error = %v", err)
		}
		if time.Since(now) != maxRateLimitWait {
			t.Fatalf("wait = %s", time.Since(now))
		}
		if !limiter.blockedUntil.Equal(now.Add(time.Hour)) {
			t.Fatal("server deadline shortened")
		}
	})
}

func TestRateLimitSharedAcrossRetriesAndNewRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		original := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				resp := response(r, 429, "")
				resp.Header.Set("Retry-After", "10")
				return resp, nil
			}
			if time.Now().Before(time.Date(2000, 1, 1, 0, 0, 10, 0, time.UTC)) {
				t.Error("request sent during cooldown")
			}
			return response(r, 204, ""), nil
		})}
		base, _ := url.Parse("https://example.test")
		client, err := NewWithBaseURL(base, "secret", original)
		if err != nil {
			t.Fatal(err)
		}
		if client.httpClient.HTTPClient == original {
			t.Fatal("modified caller's client")
		}
		done := make(chan error, 2)
		go func() {
			done <- client.DoWithoutRetry(context.Background(), http.MethodPost, "memberships", nil, nil, nil)
		}()
		synctest.Wait()
		go func() {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test/users", nil)
			resp, err := client.HTTPClient().Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			done <- err
		}()
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if calls.Load() != 3 {
			t.Fatalf("calls = %d", calls.Load())
		}
	})
}

func TestRateLimitFinalResponseStillPausesOtherRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		original := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			resp := response(r, 429, "")
			resp.Header.Set("Retry-After", "3600")
			return resp, nil
		})}
		base, _ := url.Parse("https://example.test")
		client, err := NewWithBaseURL(base, "secret", original)
		if err != nil {
			t.Fatal(err)
		}
		client.httpClient.RetryMax = 0
		if err := client.Do(context.Background(), http.MethodGet, "users", nil, nil, nil); err == nil {
			t.Fatal("expected 429")
		}
		client.httpClient.RetryMax = 4
		start := time.Now()
		err = client.Do(context.Background(), http.MethodGet, "users", nil, nil, nil)
		if !errors.Is(err, errRateLimitWait) {
			t.Fatalf("error = %v", err)
		}
		if time.Since(start) != maxRateLimitWait {
			t.Fatal("wait budget was retried")
		}
		if calls.Load() != 1 {
			t.Fatalf("calls = %d", calls.Load())
		}
	})
}
