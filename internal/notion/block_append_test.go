package notion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const appendPath = "/v1/blocks/page-1/children"

// appendBlockReply は results に quote block を n 個持つ応答。ID は blk-1, blk-2, ... になる。
func appendBlockReply(n int) string {
	results := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		results = append(results, fmt.Sprintf(`{"object":"block","id":"blk-%d","type":"quote","quote":{"rich_text":[]}}`, i))
	}
	return `{"object":"list","results":[` + strings.Join(results, ",") + `]}`
}

func quoteBlocks(n int) []QuoteBlock {
	quotes := make([]QuoteBlock, 0, n)
	for i := 1; i <= n; i++ {
		quotes = append(quotes, QuoteBlock{RichText: []RichText{{Content: fmt.Sprintf("本文 %d", i)}}})
	}
	return quotes
}

func expectedRichTexts(contents ...string) []any {
	items := []any{}
	for _, content := range contents {
		items = append(items, expectedRichText(content)...)
	}
	return items
}

func TestAppendBlockChildren(t *testing.T) {
	quotes := []QuoteBlock{
		{
			RichText: []RichText{{Content: "本文"}, {Content: "\nLocation 12 · khs:a-highlight-1"}},
			Children: []ParagraphBlock{{RichText: []RichText{{Content: "Note"}}}},
		},
		{
			RichText: []RichText{{Content: "Note だけ\nkhs:a-note-2"}},
		},
	}
	wantBody := map[string]any{
		"children": []any{
			map[string]any{
				"type": "quote",
				"quote": map[string]any{
					"rich_text": expectedRichTexts("本文", "\nLocation 12 · khs:a-highlight-1"),
					"children": []any{
						map[string]any{
							"type":      "paragraph",
							"paragraph": map[string]any{"rich_text": expectedRichTexts("Note")},
						},
					},
				},
			},
			map[string]any{
				"type":  "quote",
				"quote": map[string]any{"rich_text": expectedRichTexts("Note だけ\nkhs:a-note-2")},
			},
		},
	}
	client, requests := newQueryTestClient(t, queryReply{body: appendBlockReply(2)})
	got, err := client.AppendBlockChildren(context.Background(), "page-1", quotes)
	if err != nil {
		t.Fatalf("AppendBlockChildren() error = %v", err)
	}
	if want := []string{"blk-1", "blk-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AppendBlockChildren() = %q, want %q", got, want)
	}
	records := requests()
	if len(records) != 1 {
		t.Fatalf("requests = %d, want 1", len(records))
	}
	record := records[0]
	if record.method != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", record.method)
	}
	if record.path != appendPath {
		t.Errorf("path = %q, want %q", record.path, appendPath)
	}
	if record.authorization != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want %q", record.authorization, "Bearer "+testToken)
	}
	if record.notionVersion != "2026-03-11" {
		t.Errorf("Notion-Version = %q, want %q", record.notionVersion, "2026-03-11")
	}
	if record.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", record.contentType, "application/json")
	}
	if !reflect.DeepEqual(record.body, wantBody) {
		t.Errorf("body = %#v, want %#v", record.body, wantBody)
	}
}

func TestAppendBlockChildrenPath(t *testing.T) {
	tests := []struct {
		name string
		id   string
		path string
	}{
		{name: "通常の page ID", id: "page-1", path: appendPath},
		{name: "slash を含む page ID", id: "a/b", path: "/v1/blocks/a%2Fb/children"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newQueryTestClient(t, queryReply{body: appendBlockReply(1)})
			if _, err := client.AppendBlockChildren(context.Background(), tt.id, quoteBlocks(1)); err != nil {
				t.Fatalf("AppendBlockChildren() error = %v", err)
			}
			records := requests()
			if len(records) != 1 {
				t.Fatalf("requests = %d, want 1", len(records))
			}
			if records[0].path != tt.path {
				t.Errorf("path = %q, want %q", records[0].path, tt.path)
			}
		})
	}
}

func TestAppendBlockChildrenDoesNotRetry(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		msg    string
	}{
		{name: "429", status: http.StatusTooManyRequests, code: "rate_limited", msg: "Rate limited."},
		{name: "529", status: 529, code: "service_overload", msg: "Overloaded."},
		{name: "500", status: http.StatusInternalServerError, code: "internal_server_error", msg: "Unexpected error."},
		{name: "503", status: http.StatusServiceUnavailable, code: "service_unavailable", msg: "Unavailable."},
		{name: "504", status: http.StatusGatewayTimeout, code: "gateway_timeout", msg: "Gateway timeout."},
	}
	for _, tt := range tests {
		t.Run(tt.name+" は再送しない", func(t *testing.T) {
			body := fmt.Sprintf(`{"object":"error","status":%d,"code":%q,"message":%q}`, tt.status, tt.code, tt.msg)
			client, records, waits, logs := newRetryTestClient(t, retryReply{
				status: tt.status, header: map[string]string{"Retry-After": "1"}, body: body,
			})
			got, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(1))
			assertRetryError(t, err, fmt.Sprintf("notion: PATCH %s: HTTP %d %s: %s", appendPath, tt.status, tt.code, tt.msg))
			if got != nil {
				t.Errorf("AppendBlockChildren() = %q, want nil", got)
			}
			if got := len(records()); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
			assertRetryWaits(t, waits, nil)
			if logs.Len() != 0 {
				t.Errorf("ログ = %q, want none", logs.String())
			}
			var status *StatusError
			if !errors.As(err, &status) {
				t.Fatal("errors.As(err, &StatusError) = false, want true")
			}
			if status.Status != tt.status {
				t.Errorf("StatusError.Status = %d, want %d", status.Status, tt.status)
			}
		})
	}
}

func TestAppendBlockChildrenHTTPTimeout(t *testing.T) {
	var requests atomic.Int32
	client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}, withHTTPTimeout(50*time.Millisecond))
	got, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(1))
	assertNotionError(t, err, "notion: PATCH "+appendPath+": ", "Client.Timeout exceeded")
	if got != nil {
		t.Errorf("AppendBlockChildren() = %q, want nil", got)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
	var status *StatusError
	if errors.As(err, &status) {
		t.Errorf("errors.As(err, &StatusError) = true, want false")
	}
}

// isAuthStatus は呼び出し側が「token / 権限 / ページの有無の失敗（401 / 403 / 404）」とそれ以外を見分ける書き方。
func isAuthStatus(err error) bool {
	var status *StatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden || status.Status == http.StatusNotFound
}

func TestAppendBlockChildrenStatusError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		code       string
		wantAuth   bool
		wantStatus bool
	}{
		{name: "400 はそれ以外", status: http.StatusBadRequest, code: "validation_error", wantStatus: true},
		{name: "401 は認証の失敗", status: http.StatusUnauthorized, code: "unauthorized", wantAuth: true, wantStatus: true},
		{name: "403 は認証の失敗", status: http.StatusForbidden, code: "restricted_resource", wantAuth: true, wantStatus: true},
		{name: "404 は認証の失敗", status: http.StatusNotFound, code: "object_not_found", wantAuth: true, wantStatus: true},
		{name: "429 はそれ以外", status: http.StatusTooManyRequests, code: "rate_limited", wantStatus: true},
		{name: "500 はそれ以外", status: http.StatusInternalServerError, code: "internal_server_error", wantStatus: true},
		{name: "503 はそれ以外", status: http.StatusServiceUnavailable, code: "service_unavailable", wantStatus: true},
		{name: "529 はそれ以外", status: 529, code: "service_overload", wantStatus: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"object":"error","status":%d,"code":%q,"message":"failed"}`, tt.status, tt.code)
			client, requests := newQueryTestClient(t, queryReply{status: tt.status, body: body})
			_, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(1))
			assertNotionError(t, err)
			var status *StatusError
			if got := errors.As(err, &status); got != tt.wantStatus {
				t.Fatalf("errors.As(err, &StatusError) = %t, want %t", got, tt.wantStatus)
			}
			if status.Status != tt.status || status.Code != tt.code {
				t.Errorf("StatusError = (%d, %q), want (%d, %q)", status.Status, status.Code, tt.status, tt.code)
			}
			if got := isAuthStatus(err); got != tt.wantAuth {
				t.Errorf("isAuthStatus(err) = %t, want %t", got, tt.wantAuth)
			}
			if got := len(requests()); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}

	t.Run("タイムアウトはそれ以外", func(t *testing.T) {
		client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		}, withHTTPTimeout(50*time.Millisecond))
		_, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(1))
		assertNotionError(t, err)
		if got := isAuthStatus(err); got {
			t.Errorf("isAuthStatus(err) = %t, want false", got)
		}
	})
}

func TestAppendBlockChildrenLimit(t *testing.T) {
	t.Run("100 個は送る", func(t *testing.T) {
		client, requests := newQueryTestClient(t, queryReply{body: appendBlockReply(100)})
		got, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(100))
		if err != nil {
			t.Fatalf("AppendBlockChildren() error = %v", err)
		}
		if len(got) != 100 || got[0] != "blk-1" || got[99] != "blk-100" {
			t.Errorf("AppendBlockChildren() = %d IDs (%q ... %q), want 100 IDs blk-1 ... blk-100", len(got), got[0], got[len(got)-1])
		}
		if got := len(requests()); got != 1 {
			t.Errorf("requests = %d, want 1", got)
		}
	})

	t.Run("101 個は送らずに error", func(t *testing.T) {
		client, requests := newQueryTestClient(t)
		got, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(101))
		assertNotionError(t, err)
		const want = "notion: PATCH " + appendPath + ": children must be at most 100 blocks, got 101"
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
		if got != nil {
			t.Errorf("AppendBlockChildren() = %q, want nil", got)
		}
		if got := len(requests()); got != 0 {
			t.Errorf("requests = %d, want 0", got)
		}
	})
}

func TestAppendBlockChildrenInvalidResponse(t *testing.T) {
	const block = `{"object":"block","id":"blk-1","type":"quote","quote":{"rich_text":[]}}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object がない", body: `{"results":[` + block + `]}`, want: "invalid response: object is required"},
		{name: "object が list でない", body: `{"object":"error","results":[` + block + `]}`, want: "invalid response: object must be list"},
		{name: "results がない", body: `{"object":"list"}`, want: "invalid response: results is required"},
		{name: "results が送った数より少ない", body: `{"object":"list","results":[]}`, want: "invalid response: results has 0 blocks, want 1"},
		{name: "results が送った数より多い", body: appendBlockReply(2), want: "invalid response: results has 2 blocks, want 1"},
		{name: "id が空", body: `{"object":"list","results":[{"object":"block","id":"","type":"quote","quote":{"rich_text":[]}}]}`, want: "invalid response: results[0].id must not be empty"},
		{name: "id がない", body: `{"object":"list","results":[{"object":"block","type":"quote","quote":{"rich_text":[]}}]}`, want: "invalid response: results[0].id is required"},
		{name: "object が block でない", body: `{"object":"list","results":[{"object":"page","id":"blk-1","type":"quote","quote":{"rich_text":[]}}]}`, want: "invalid response: results[0].object must be block"},
		{name: "type が quote でない", body: `{"object":"list","results":[{"object":"block","id":"blk-1","type":"paragraph","paragraph":{"rich_text":[]}}]}`, want: "invalid response: results[0].type must be quote"},
		{name: "null", body: `null`, want: "invalid response: object is required"},
		{name: "JSON ではない", body: `<html>bad gateway</html>`, want: "invalid response JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newQueryTestClient(t, queryReply{body: tt.body})
			got, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(1))
			assertNotionError(t, err)
			want := "notion: PATCH " + appendPath + ": " + tt.want
			if got := err.Error(); got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
			if got != nil {
				t.Errorf("AppendBlockChildren() = %q, want nil", got)
			}
			if got := len(requests()); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestAppendBlockChildrenRedactsToken(t *testing.T) {
	t.Run("page ID の token を隠す", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{
			status: http.StatusNotFound,
			body:   `{"code":"object_not_found","message":"Could not find block."}`,
		})
		_, err := client.AppendBlockChildren(context.Background(), testToken, quoteBlocks(1))
		assertNotionError(t, err)
		const want = "notion: PATCH /v1/blocks/[REDACTED]/children: HTTP 404 object_not_found: Could not find block."
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})

	t.Run("StatusError の message 内の token を隠す", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{
			status: http.StatusBadRequest,
			body:   `{"code":"validation_error","message":"invalid test-token-value"}`,
		})
		_, err := client.AppendBlockChildren(context.Background(), "page-1", quoteBlocks(1))
		assertNotionError(t, err)
		const want = "notion: PATCH " + appendPath + ": HTTP 400 validation_error: invalid [REDACTED]"
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
		var status *StatusError
		if !errors.As(err, &status) {
			t.Fatal("errors.As(err, &StatusError) = false, want true")
		}
		if got, want := status.Error(), "HTTP 400 validation_error: invalid [REDACTED]"; got != want {
			t.Errorf("StatusError.Error() = %q, want %q", got, want)
		}
	})

	t.Run("タイムアウトの error chain に token を含めない", func(t *testing.T) {
		client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		}, withHTTPTimeout(50*time.Millisecond))
		_, err := client.AppendBlockChildren(context.Background(), testToken, quoteBlocks(1))
		assertNotionError(t, err)
		assertChainHidesToken(t, err)
	})
}
