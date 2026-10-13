package notion

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func statusHandler(status int, retryAfter string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"object":"error","code":"x","message":"m"}`))
	}
}

func appendOnce(c *Client, ctx context.Context) error {
	return c.doOnce(ctx, http.MethodPatch, "/v1/blocks/b/children", map[string]any{}, &struct{}{})
}

func TestOutcomeUnknown(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{"429 は結果不明", 429, true},
		{"529 は結果不明", 529, true},
		{"500 は結果不明", 500, true},
		{"502 は結果不明", 502, true},
		{"503 は結果不明", 503, true},
		{"504 は結果不明", 504, true},
		{"400 は結果不明でない", 400, false},
		{"401 は結果不明でない", 401, false},
		{"403 は結果不明でない", 403, false},
		{"404 は結果不明でない", 404, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, statusHandler(tt.status, ""))
			err := appendOnce(client, context.Background())
			assertNotionError(t, err)
			if got := OutcomeUnknown(err); got != tt.want {
				t.Errorf("OutcomeUnknown = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("再送を使い切った 429 / 529", func(t *testing.T) {
		client := newTestClient(t, statusHandler(529, ""), withRetrySleep(func(context.Context, time.Duration) error { return nil }), WithLogWriter(io.Discard))
		err := client.get(context.Background(), "/v1/x", &struct{}{})
		if !OutcomeUnknown(err) {
			t.Errorf("OutcomeUnknown = false, err = %v", err)
		}
	})

	t.Run("HTTP のタイムアウト", func(t *testing.T) {
		client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}, withHTTPTimeout(50*time.Millisecond))
		err := appendOnce(client, context.Background())
		if !OutcomeUnknown(err) {
			t.Errorf("OutcomeUnknown = false, err = %v", err)
		}
	})

	t.Run("応答の形の異常", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) })
		if err := appendOnce(client, context.Background()); err == nil || OutcomeUnknown(err) {
			t.Errorf("OutcomeUnknown = true, err = %v", err)
		}
	})

	t.Run("ctx の取消", func(t *testing.T) {
		client := newTestClient(t, statusHandler(200, ""))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := appendOnce(client, ctx); err == nil || OutcomeUnknown(err) {
			t.Errorf("OutcomeUnknown = true, err = %v", err)
		}
	})

	t.Run("ctx の期限切れ", func(t *testing.T) {
		client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if err := appendOnce(client, ctx); err == nil || OutcomeUnknown(err) {
			t.Errorf("OutcomeUnknown = true, err = %v", err)
		}
	})

	t.Run("nil と Notion の client が返したものでない error", func(t *testing.T) {
		if OutcomeUnknown(nil) {
			t.Error("OutcomeUnknown(nil) = true")
		}
		if OutcomeUnknown(errors.New("HTTP 503")) {
			t.Error("OutcomeUnknown(other) = true")
		}
		if OutcomeUnknown(&StatusError{Status: 503}) {
			t.Error("OutcomeUnknown(StatusError) = true")
		}
	})
}

func TestRetryAfter(t *testing.T) {
	t.Run("429 の Retry-After", func(t *testing.T) {
		client := newTestClient(t, statusHandler(429, "7"))
		d, ok := RetryAfter(appendOnce(client, context.Background()))
		if !ok || d != 7*time.Second {
			t.Errorf("RetryAfter = %s, %v, want 7s, true", d, ok)
		}
	})
	t.Run("Duration に収まる最大の秒数の Retry-After", func(t *testing.T) {
		client := newTestClient(t, statusHandler(429, strconv.Itoa(maxDurationSeconds)))
		d, ok := RetryAfter(appendOnce(client, context.Background()))
		if !ok || d != time.Duration(maxDurationSeconds)*time.Second || d < 0 {
			t.Errorf("RetryAfter = %s, %v, want max, true", d, ok)
		}
	})
	t.Run("Duration に収まらない秒数の Retry-After は取り出せない", func(t *testing.T) {
		client := newTestClient(t, statusHandler(429, strconv.Itoa(maxDurationSeconds+1)))
		if d, ok := RetryAfter(appendOnce(client, context.Background())); ok {
			t.Errorf("RetryAfter = %s, true, want false", d)
		}
	})
	t.Run("Retry-After が無い 429", func(t *testing.T) {
		client := newTestClient(t, statusHandler(429, ""))
		if d, ok := RetryAfter(appendOnce(client, context.Background())); ok {
			t.Errorf("RetryAfter = %s, true, want false", d)
		}
	})
	t.Run("429 以外", func(t *testing.T) {
		client := newTestClient(t, statusHandler(503, "7"))
		if d, ok := RetryAfter(appendOnce(client, context.Background())); ok {
			t.Errorf("RetryAfter = %s, true, want false", d)
		}
	})
	t.Run("nil", func(t *testing.T) {
		if _, ok := RetryAfter(nil); ok {
			t.Error("RetryAfter(nil) ok = true")
		}
	})
}
