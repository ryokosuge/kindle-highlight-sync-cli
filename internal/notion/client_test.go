package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

const testToken = "test-token-value"

func newTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(testToken, append([]Option{WithBaseURL(srv.URL)}, opts...)...)
}

func assertNotionError(t *testing.T, err error, wantParts ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want Notion error")
	}
	if got := exitcode.Code(err); got != 4 {
		t.Errorf("exitcode.Code(err) = %d, want 4", got)
	}
	for _, part := range wantParts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error = %q, want it to contain %q", err, part)
		}
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error = %q, must not contain token", err)
	}
}

func TestHeaders(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Client) error
	}{
		{
			name: "Database",
			path: "/v1/databases/db-1",
			body: `{"object":"database","id":"db-1","data_sources":[]}`,
			call: func(c *Client) error {
				_, err := c.RetrieveDatabase(context.Background(), "db-1")
				return err
			},
		},
		{
			name: "Data Source",
			path: "/v1/data_sources/ds-1",
			body: `{"object":"data_source","id":"ds-1","properties":{}}`,
			call: func(c *Client) error {
				_, err := c.RetrieveDataSource(context.Background(), "ds-1")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			var gotHeader http.Header
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotHeader = r.Header.Clone()
				_, _ = w.Write([]byte(tt.body))
			})

			if err := tt.call(client); err != nil {
				t.Fatalf("request error = %v", err)
			}
			if gotMethod != http.MethodGet {
				t.Errorf("method = %q, want GET", gotMethod)
			}
			if gotPath != tt.path {
				t.Errorf("path = %q, want %q", gotPath, tt.path)
			}
			if got := gotHeader.Get("Authorization"); got != "Bearer test-token-value" {
				t.Errorf("Authorization = %q, want %q", got, "Bearer test-token-value")
			}
			if got := gotHeader.Get("Notion-Version"); got != "2026-03-11" {
				t.Errorf("Notion-Version = %q, want %q", got, "2026-03-11")
			}
		})
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "認証エラー",
			id:         "db-1",
			statusCode: http.StatusUnauthorized,
			body:       `{"object":"error","status":401,"code":"unauthorized","message":"API token is invalid."}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 401 unauthorized: API token is invalid.",
		},
		{
			name:       "対象なし",
			id:         "db-1",
			statusCode: http.StatusNotFound,
			body:       `{"object":"error","status":404,"code":"object_not_found","message":"Could not find database."}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 404 object_not_found: Could not find database.",
		},
		{
			name:       "サーバーエラー",
			id:         "db-1",
			statusCode: http.StatusInternalServerError,
			body:       `{"object":"error","status":500,"code":"internal_server_error","message":"Unexpected error."}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 500 internal_server_error: Unexpected error.",
		},
		{
			name:       "エラーメッセージ内のトークンを隠す",
			id:         "db-1",
			statusCode: http.StatusUnauthorized,
			body:       `{"code":"unauthorized","message":"bad token test-token-value"}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 401 unauthorized: bad token [REDACTED]",
		},
		{
			name:       "JSON ではないエラー応答",
			id:         "db-1",
			statusCode: http.StatusBadGateway,
			body:       `<html>bad gateway</html>`,
			want:       "notion: GET /v1/databases/db-1: HTTP 502",
		},
		{
			name:       "ID の token を隠す",
			id:         testToken,
			statusCode: http.StatusNotFound,
			body:       `{"code":"object_not_found","message":"Could not find database."}`,
			want:       "notion: GET /v1/databases/[REDACTED]: HTTP 404 object_not_found: Could not find database.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			})

			_, err := client.RetrieveDatabase(context.Background(), tt.id)
			assertNotionError(t, err)
			if got := err.Error(); got != tt.want {
				t.Errorf("error = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedirect(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{name: "301", statusCode: http.StatusMovedPermanently},
		{name: "302", statusCode: http.StatusFound},
		{name: "307", statusCode: http.StatusTemporaryRedirect},
		{name: "308", statusCode: http.StatusPermanentRedirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var movedHit atomic.Bool
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/moved" {
					movedHit.Store(true)
					_, _ = w.Write([]byte(`{"object":"database","id":"db-1","data_sources":[]}`))
					return
				}
				w.Header().Set("Location", "/moved")
				w.WriteHeader(tt.statusCode)
			})

			_, err := client.RetrieveDatabase(context.Background(), "db-1")
			assertNotionError(t, err)
			want := "notion: GET /v1/databases/db-1: HTTP " + tt.name
			if got := err.Error(); got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
			if movedHit.Load() {
				t.Error("redirect target was requested")
			}
		})
	}
}

func TestConnectionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close()

	client := New(testToken, WithBaseURL(baseURL))
	t.Run("通信できない", func(t *testing.T) {
		_, err := client.RetrieveDatabase(context.Background(), "db-1")
		assertNotionError(t, err, "notion: GET /v1/databases/db-1")
	})
	t.Run("ID の token を隠す", func(t *testing.T) {
		_, err := client.RetrieveDatabase(context.Background(), testToken)
		assertNotionError(t, err, "notion: GET /v1/databases/[REDACTED]: ")
	})
}

func TestCanceledContext(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.RetrieveDatabase(ctx, "db-1")
	assertNotionError(t, err, "notion: GET /v1/databases/db-1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false, err = %v", err)
	}
}

func TestDeadlineExceeded(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := client.RetrieveDatabase(ctx, "db-1")
	assertNotionError(t, err, "notion: GET /v1/databases/db-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false, err = %v", err)
	}
}

func assertChainHidesToken(t *testing.T, err error) {
	t.Helper()
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), testToken) {
			t.Errorf("error chain contains token: %q", e)
		}
	}
	if errors.As(err, new(*url.Error)) {
		t.Error("errors.As(err, new(*url.Error)) = true, want false")
	}
}

func TestErrorChainDoesNotContainToken(t *testing.T) {
	t.Run("通信失敗", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		baseURL := srv.URL
		srv.Close()

		client := New(testToken, WithBaseURL(baseURL))
		_, err := client.RetrieveDatabase(context.Background(), testToken)
		assertNotionError(t, err)
		assertChainHidesToken(t, err)
	})

	t.Run("取り消し", func(t *testing.T) {
		client := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.RetrieveDatabase(ctx, testToken)
		assertNotionError(t, err)
		assertChainHidesToken(t, err)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("errors.Is(err, context.Canceled) = false, err = %v", err)
		}
	})
}

func TestBodyReadFailure(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte(`{"object":`))
	})

	_, err := client.RetrieveDatabase(context.Background(), testToken)
	assertNotionError(t, err, "notion: GET /v1/databases/[REDACTED]: ", "unexpected EOF")
}

func TestInvalidResponseJSON(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "通常の ID",
			id:   "db-1",
			want: "notion: GET /v1/databases/db-1: invalid response JSON",
		},
		{
			name: "ID の token を隠す",
			id:   testToken,
			want: "notion: GET /v1/databases/[REDACTED]: invalid response JSON",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"object":`))
			})
			_, err := client.RetrieveDatabase(context.Background(), tt.id)
			assertNotionError(t, err)
			if got := err.Error(); got != tt.want {
				t.Errorf("error = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInvalidResponseRedactsToken(t *testing.T) {
	t.Run("ID の token を隠す", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"page","id":"x","data_sources":[]}`))
		})
		_, err := client.RetrieveDatabase(context.Background(), testToken)
		assertNotionError(t, err)
		const want = "notion: GET /v1/databases/[REDACTED]: invalid response: object must be database"
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})

	t.Run("応答中の token を隠す", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"data_source","id":"ds","properties":{"test-token-value":{"id":"n","name":"Other","type":"title"}}}`))
		})
		_, err := client.RetrieveDataSource(context.Background(), "ds")
		assertNotionError(t, err)
		const want = `notion: GET /v1/data_sources/ds: invalid response: properties["[REDACTED]"]: name "Other" does not match key`
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})
}
