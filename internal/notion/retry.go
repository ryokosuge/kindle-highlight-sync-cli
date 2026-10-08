package notion

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"time"
)

const (
	maxRetries            = 5
	baseBackoff           = time.Second
	maxBackoff            = 30 * time.Second
	maxRetryAfter         = 60 * time.Second
	statusServiceOverload = 529
)

// retryPolicy は 429 / 529 のあと待つ長さと待ち方を決める。テストでは sleep と jitter を差し替える。
type retryPolicy struct {
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func() time.Duration
	log    io.Writer
}

func retryable(status int) bool {
	return status == 429 || status == statusServiceOverload
}

func parseRetryAfter(v string) (secs int, ok bool) {
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0, false
	}
	return secs, true
}

func (p retryPolicy) waitFor(n int, retryAfter time.Duration, ok bool) time.Duration {
	if ok && n == 1 {
		return retryAfter
	}
	backoff := baseBackoff
	for i := 1; i < n && backoff < maxBackoff; i++ {
		backoff *= 2
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	backoff += p.jitter()
	if ok && retryAfter > backoff {
		return retryAfter
	}
	return backoff
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func defaultJitter() time.Duration {
	return rand.N(time.Second)
}

func (p retryPolicy) logWait(status, n int, d time.Duration) {
	_, _ = fmt.Fprintf(p.log, "notion: HTTP %d: retry %d/%d after %s\n", status, n, maxRetries, d.Round(time.Millisecond))
}
