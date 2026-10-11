package notion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type pageWriteCall struct {
	name, method, path string
	call               func(*Client) error
}

func pageWriteCalls() []pageWriteCall {
	return []pageWriteCall{
		{
			name:   "作成",
			method: http.MethodPost,
			path:   "/v1/pages",
			call: func(c *Client) error {
				_, err := c.CreatePage(context.Background(), "ds-1", map[string]PropertyInput{
					"Title": TitleInput("本のタイトル"),
				})
				return err
			},
		},
		{
			name:   "更新",
			method: http.MethodPatch,
			path:   "/v1/pages/page-1",
			call: func(c *Client) error {
				return c.UpdatePage(context.Background(), "page-1", map[string]PropertyInput{
					"Title": TitleInput("本のタイトル"),
				})
			},
		},
	}
}

func expectedRichText(text string) []any {
	if text == "" {
		return []any{}
	}
	return []any{
		map[string]any{
			"type": "text",
			"text": map[string]any{"content": text},
		},
	}
}

func TestCreatePage(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "完全な page 応答", response: `{"object":"page","id":"page-created","properties":{},"url":"https://notion.test/page-created"}`},
		{name: "部分 page 応答", response: `{"object":"page","id":"page-created"}`},
	}
	properties := map[string]PropertyInput{
		"title": TitleInput("本のタイトル"),
		"%3Axs": RichTextInput("著者"),
		"Cd34":  RichTextInput("B000000001"),
		"Ef56":  DateInput("2026-10-09"),
		"Gh78":  RichTextInput(""),
		"Ij90":  DateInput(""),
	}
	wantBody := map[string]any{
		"parent": map[string]any{
			"type":           "data_source_id",
			"data_source_id": "ds-113",
		},
		"properties": map[string]any{
			"title": map[string]any{"title": expectedRichText("本のタイトル")},
			"%3Axs": map[string]any{"rich_text": expectedRichText("著者")},
			"Cd34":  map[string]any{"rich_text": expectedRichText("B000000001")},
			"Ef56":  map[string]any{"date": map[string]any{"start": "2026-10-09"}},
			"Gh78":  map[string]any{"rich_text": []any{}},
			"Ij90":  map[string]any{"date": nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newQueryTestClient(t, queryReply{body: tt.response})
			got, err := client.CreatePage(context.Background(), "ds-113", properties)
			if err != nil {
				t.Fatalf("CreatePage() error = %v", err)
			}
			if got != "page-created" {
				t.Errorf("CreatePage() = %q, want %q", got, "page-created")
			}
			records := requests()
			if len(records) != 1 {
				t.Fatalf("requests = %d, want 1", len(records))
			}
			record := records[0]
			if record.method != http.MethodPost {
				t.Errorf("method = %q, want POST", record.method)
			}
			if record.path != "/v1/pages" {
				t.Errorf("path = %q, want %q", record.path, "/v1/pages")
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
		})
	}
}

func TestUpdatePage(t *testing.T) {
	tests := []struct {
		name string
		id   string
		path string
	}{
		{name: "通常の page ID", id: "page-1", path: "/v1/pages/page-1"},
		{name: "slash を含む page ID", id: "a/b", path: "/v1/pages/a%2Fb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newQueryTestClient(t, queryReply{body: `{"object":"page","id":"page-1"}`})
			properties := map[string]PropertyInput{"title": TitleInput("更新"), "date": DateInput("")}
			if err := client.UpdatePage(context.Background(), tt.id, properties); err != nil {
				t.Fatalf("UpdatePage() error = %v", err)
			}
			records := requests()
			if len(records) != 1 {
				t.Fatalf("requests = %d, want 1", len(records))
			}
			if records[0].method != http.MethodPatch {
				t.Errorf("method = %q, want PATCH", records[0].method)
			}
			if records[0].path != tt.path {
				t.Errorf("path = %q, want %q", records[0].path, tt.path)
			}
			wantBody := map[string]any{
				"properties": map[string]any{
					"title": map[string]any{"title": expectedRichText("更新")},
					"date":  map[string]any{"date": nil},
				},
			}
			if !reflect.DeepEqual(records[0].body, wantBody) {
				t.Errorf("body = %#v, want %#v", records[0].body, wantBody)
			}
		})
	}
}

func TestPageWriteRetryRecovery(t *testing.T) {
	for _, call := range pageWriteCalls() {
		t.Run(call.name, func(t *testing.T) {
			client, records, waits, _ := newRetryTestClient(t,
				retryReply{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "1"}, body: rateLimited},
				retryReply{status: 529, body: serviceOverload},
				retryReply{body: `{"object":"page","id":"page-1"}`},
			)
			if err := call.call(client); err != nil {
				t.Fatalf("page write error = %v", err)
			}
			got := records()
			if len(got) != 3 {
				t.Fatalf("requests = %d, want 3", len(got))
			}
			for i, record := range got {
				if record.method != call.method {
					t.Errorf("requests[%d].method = %q, want %q", i, record.method, call.method)
				}
				if record.path != call.path {
					t.Errorf("requests[%d].path = %q, want %q", i, record.path, call.path)
				}
				if i > 0 && record.body != got[0].body {
					t.Errorf("requests[%d].body = %q, want %q", i, record.body, got[0].body)
				}
			}
			assertRetryWaits(t, waits, []time.Duration{time.Second, 2500 * time.Millisecond})
		})
	}
}

func TestPageWriteDoesNotRetryStatuses(t *testing.T) {
	statuses := []struct {
		name   string
		status int
		code   string
		msg    string
	}{
		{name: "500", status: http.StatusInternalServerError, code: "internal_server_error", msg: "Unexpected error."},
		{name: "503", status: http.StatusServiceUnavailable, code: "service_unavailable", msg: "Unavailable."},
		{name: "504", status: http.StatusGatewayTimeout, code: "gateway_timeout", msg: "Gateway timeout."},
	}
	for _, call := range pageWriteCalls() {
		for _, status := range statuses {
			t.Run(call.name+" "+status.name, func(t *testing.T) {
				body := fmt.Sprintf(`{"object":"error","status":%d,"code":%q,"message":%q}`, status.status, status.code, status.msg)
				client, records, waits, _ := newRetryTestClient(t, retryReply{
					status: status.status, header: map[string]string{"Retry-After": "1"}, body: body,
				})
				err := call.call(client)
				assertRetryError(t, err, fmt.Sprintf("notion: %s %s: HTTP %d %s: %s", call.method, call.path, status.status, status.code, status.msg))
				if got := len(records()); got != 1 {
					t.Errorf("requests = %d, want 1", got)
				}
				assertRetryWaits(t, waits, nil)
			})
		}
	}
}

func TestPageWriteHTTPTimeout(t *testing.T) {
	var requests atomic.Int32
	client := newTestClient(t, func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}, withHTTPTimeout(50*time.Millisecond))
	err := client.UpdatePage(context.Background(), "page-1", map[string]PropertyInput{"title": TitleInput("本")})
	assertNotionError(t, err, "Client.Timeout exceeded")
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestPageWriteInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object がない", body: `{"id":"p"}`, want: "invalid response: object is required"},
		{name: "202 の async_task", body: `{"object":"async_task","id":"t"}`, want: "invalid response: object must be page"},
		{name: "object が page でない", body: `{"object":"database","id":"p"}`, want: "invalid response: object must be page"},
		{name: "id がない", body: `{"object":"page"}`, want: "invalid response: id is required"},
		{name: "id が空", body: `{"object":"page","id":""}`, want: "invalid response: id must not be empty"},
		{name: "null", body: `null`, want: "invalid response: object is required"},
		{name: "JSON ではない", body: `<html>bad gateway</html>`, want: "invalid response JSON"},
	}
	for _, call := range pageWriteCalls() {
		for _, tt := range tests {
			t.Run(call.name+" "+tt.name, func(t *testing.T) {
				client, records := newQueryTestClient(t, queryReply{status: http.StatusAccepted, body: tt.body})
				err := call.call(client)
				assertNotionError(t, err)
				want := fmt.Sprintf("notion: %s %s: %s", call.method, call.path, tt.want)
				if got := err.Error(); got != want {
					t.Errorf("error = %q, want %q", got, want)
				}
				if got := len(records()); got != 1 {
					t.Errorf("requests = %d, want 1", got)
				}
			})
		}
	}
}

func TestStatusErrorExtraction(t *testing.T) {
	validationMessage := "body failed validation: body.properties.title.title[0].text.content.length should be ≤ `2000`, instead was `2001`."
	tests := []struct {
		name    string
		status  int
		code    string
		message string
		body    string
	}{
		{
			name:    "validation_error の応答",
			status:  http.StatusBadRequest,
			code:    "validation_error",
			message: validationMessage,
			body:    fmt.Sprintf(`{"object":"error","status":400,"code":"validation_error","message":%q}`, validationMessage),
		},
		{
			name:    "unauthorized の応答",
			status:  http.StatusUnauthorized,
			code:    "unauthorized",
			message: "API token is invalid.",
			body:    `{"object":"error","status":401,"code":"unauthorized","message":"API token is invalid."}`,
		},
		{
			name:    "restricted_resource の応答",
			status:  http.StatusForbidden,
			code:    "restricted_resource",
			message: "You do not have access to this resource.",
			body:    `{"object":"error","status":403,"code":"restricted_resource","message":"You do not have access to this resource."}`,
		},
		{
			name:    "object_not_found の応答",
			status:  http.StatusNotFound,
			code:    "object_not_found",
			message: "Could not find page.",
			body:    `{"object":"error","status":404,"code":"object_not_found","message":"Could not find page."}`,
		},
		{
			name:    "internal_server_error の応答",
			status:  http.StatusInternalServerError,
			code:    "internal_server_error",
			message: "Unexpected error.",
			body:    `{"object":"error","status":500,"code":"internal_server_error","message":"Unexpected error."}`,
		},
		{
			name:    "service_unavailable の応答",
			status:  http.StatusServiceUnavailable,
			code:    "service_unavailable",
			message: "Notion is temporarily unavailable.",
			body:    `{"object":"error","status":503,"code":"service_unavailable","message":"Notion is temporarily unavailable."}`,
		},
		{name: "非 JSON の応答", status: http.StatusBadGateway, body: `<html>bad gateway</html>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newQueryTestClient(t, queryReply{status: tt.status, body: tt.body})
			err := client.UpdatePage(context.Background(), "page-1", map[string]PropertyInput{"title": TitleInput("本")})
			assertNotionError(t, err)
			var got *StatusError
			if !errors.As(err, &got) {
				t.Fatal("errors.As(err, &StatusError) = false, want true")
			}
			if got.Status != tt.status {
				t.Errorf("StatusError.Status = %d, want %d", got.Status, tt.status)
			}
			if got.Code != tt.code {
				t.Errorf("StatusError.Code = %q, want %q", got.Code, tt.code)
			}
			statusMessage := fmt.Sprintf("HTTP %d", tt.status)
			if tt.code != "" {
				statusMessage += " " + tt.code
				if tt.message != "" {
					statusMessage += ": " + tt.message
				}
			}
			want := fmt.Sprintf("notion: PATCH /v1/pages/page-1: %s", statusMessage)
			if got := err.Error(); got != want {
				t.Errorf("error = %q, want %q", got, want)
			}
		})
	}

	t.Run("通信失敗では抽出できない", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		baseURL := srv.URL
		srv.Close()
		client := New(testToken, WithBaseURL(baseURL))
		err := client.UpdatePage(context.Background(), "page-1", map[string]PropertyInput{"title": TitleInput("本")})
		assertNotionError(t, err)
		var got *StatusError
		if errors.As(err, &got) {
			t.Errorf("errors.As(err, &StatusError) = true, want false")
		}
	})

	t.Run("invalid response では抽出できない", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{body: `{"id":"p"}`})
		err := client.UpdatePage(context.Background(), "page-1", map[string]PropertyInput{"title": TitleInput("本")})
		assertNotionError(t, err)
		var got *StatusError
		if errors.As(err, &got) {
			t.Errorf("errors.As(err, &StatusError) = true, want false")
		}
	})
}

func TestUpdatePageRedactsToken(t *testing.T) {
	t.Run("page ID の token を隠す", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{
			status: http.StatusNotFound,
			body:   `{"code":"object_not_found","message":"Could not find page."}`,
		})
		err := client.UpdatePage(context.Background(), testToken, map[string]PropertyInput{"title": TitleInput("本")})
		assertNotionError(t, err)
		const want = "notion: PATCH /v1/pages/[REDACTED]: HTTP 404 object_not_found: Could not find page."
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})

	t.Run("StatusError の message 内の token を隠す", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{
			status: http.StatusBadRequest,
			body:   `{"code":"validation_error","message":"invalid test-token-value"}`,
		})
		err := client.UpdatePage(context.Background(), testToken, map[string]PropertyInput{"title": TitleInput("本")})
		assertNotionError(t, err)
		const want = "notion: PATCH /v1/pages/[REDACTED]: HTTP 400 validation_error: invalid [REDACTED]"
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
		var status *StatusError
		if !errors.As(err, &status) {
			t.Fatal("errors.As(err, &StatusError) = false, want true")
		}
		const wantStatus = "HTTP 400 validation_error: invalid [REDACTED]"
		if got := status.Error(); got != wantStatus {
			t.Errorf("StatusError.Error() = %q, want %q", got, wantStatus)
		}
		for name, got := range map[string]string{"error": err.Error(), "StatusError": status.Error()} {
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("%s contains [REDACTED] = false, want true", name)
			}
			if strings.Contains(got, testToken) {
				t.Errorf("%s contains token = true, want false", name)
			}
		}
	})

	t.Run("通信失敗の error chain に token を含めない", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		baseURL := srv.URL
		srv.Close()
		client := New(testToken, WithBaseURL(baseURL))
		err := client.UpdatePage(context.Background(), testToken, map[string]PropertyInput{"title": TitleInput("本")})
		assertNotionError(t, err)
		assertChainHidesToken(t, err)
	})
}
