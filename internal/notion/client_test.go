package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

const testToken = "test-token-value"

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(testToken, WithBaseURL(srv.URL))
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
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "認証エラー",
			statusCode: http.StatusUnauthorized,
			body:       `{"object":"error","status":401,"code":"unauthorized","message":"API token is invalid."}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 401 unauthorized: API token is invalid.",
		},
		{
			name:       "対象なし",
			statusCode: http.StatusNotFound,
			body:       `{"object":"error","status":404,"code":"object_not_found","message":"Could not find database."}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 404 object_not_found: Could not find database.",
		},
		{
			name:       "サーバーエラー",
			statusCode: http.StatusInternalServerError,
			body:       `{"object":"error","status":500,"code":"internal_server_error","message":"Unexpected error."}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 500 internal_server_error: Unexpected error.",
		},
		{
			name:       "エラーメッセージ内のトークンを隠す",
			statusCode: http.StatusUnauthorized,
			body:       `{"code":"unauthorized","message":"bad token test-token-value"}`,
			want:       "notion: GET /v1/databases/db-1: HTTP 401 unauthorized: bad token [REDACTED]",
		},
		{
			name:       "JSON ではないエラー応答",
			statusCode: http.StatusBadGateway,
			body:       `<html>bad gateway</html>`,
			want:       "notion: GET /v1/databases/db-1: HTTP 502",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			})

			_, err := client.RetrieveDatabase(context.Background(), "db-1")
			assertNotionError(t, err)
			if got := err.Error(); got != tt.want {
				t.Errorf("error = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConnectionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close()

	client := New(testToken, WithBaseURL(baseURL))
	_, err := client.RetrieveDatabase(context.Background(), "db-1")
	assertNotionError(t, err, "notion: GET /v1/databases/db-1")
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

func TestInvalidResponseJSON(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":`))
	})

	_, err := client.RetrieveDatabase(context.Background(), "db-1")
	assertNotionError(t, err)
	const want = "notion: GET /v1/databases/db-1: invalid response JSON"
	if got := err.Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}
