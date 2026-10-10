package notion

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

type blocksRequestRecord struct {
	method, path, rawQuery, authorization, notionVersion, contentType string
	bodyLen                                                           int
}

// newBlocksTestClient は要求ごとに replies を順に返すサーバーにつないだ Client と、受けた要求の記録を返す。
func newBlocksTestClient(t *testing.T, replies ...queryReply) (*Client, func() []blocksRequestRecord) {
	t.Helper()
	var mu sync.Mutex
	var records []blocksRequestRecord
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		record := blocksRequestRecord{
			method:        r.Method,
			path:          r.URL.EscapedPath(),
			rawQuery:      r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"),
			notionVersion: r.Header.Get("Notion-Version"),
			contentType:   r.Header.Get("Content-Type"),
			bodyLen:       len(raw),
		}
		mu.Lock()
		records = append(records, record)
		n := len(records)
		mu.Unlock()
		if n > len(replies) {
			t.Errorf("unexpected request #%d", n)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reply := replies[n-1]
		if reply.status != 0 {
			w.WriteHeader(reply.status)
		}
		_, _ = w.Write([]byte(reply.body))
	})
	return client, func() []blocksRequestRecord {
		mu.Lock()
		defer mu.Unlock()
		return append([]blocksRequestRecord(nil), records...)
	}
}

const (
	firstChildrenPage = `{"object":"list","type":"block","block":{},"results":[
		{"object":"block","id":"block-1","type":"paragraph","has_children":true,
			"paragraph":{"rich_text":[{"type":"text","text":{"content":"First"},"plain_text":"First"}],"color":"default"}},
		{"object":"block","id":"block-2","type":"quote","has_children":false,
			"quote":{"rich_text":[{"type":"text","text":{"content":"Second"},"plain_text":"Second"}],"color":"default"}}
	],"has_more":true,"next_cursor":"cursor-1"}`
	lastChildrenPage = `{"object":"list","type":"block","block":{},"results":[],"has_more":false,"next_cursor":null}`
)

func TestListBlockChildren(t *testing.T) {
	t.Run("next_cursor を辿って最後のページまで返す", func(t *testing.T) {
		client, requests := newBlocksTestClient(t,
			queryReply{body: firstChildrenPage},
			queryReply{body: `{"object":"list","type":"block","block":{},"results":[
				{"object":"block","id":"block-3","type":"heading_2","has_children":false,
					"heading_2":{"rich_text":[{"type":"text","plain_text":"Third"}],"color":"default"}}
			],"has_more":true,"next_cursor":"c+2/=?&"}`},
			queryReply{body: `{"object":"list","type":"block","block":{},"results":[
				{"object":"block","id":"block-4","type":"paragraph","has_children":false,
					"paragraph":{"rich_text":[{"type":"text","plain_text":"Fourth"}],"color":"default"}}
			],"has_more":false,"next_cursor":null}`},
		)

		got, err := client.ListBlockChildren(context.Background(), "page-id")
		if err != nil {
			t.Fatalf("ListBlockChildren() error = %v", err)
		}
		want := []Block{
			{ID: "block-1", Type: "paragraph", Text: "First"},
			{ID: "block-2", Type: "quote", Text: "Second"},
			{ID: "block-3", Type: "heading_2", Text: "Third"},
			{ID: "block-4", Type: "paragraph", Text: "Fourth"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ListBlockChildren() = %#v, want %#v", got, want)
		}

		wantQueries := []url.Values{
			{"page_size": {"100"}},
			{"page_size": {"100"}, "start_cursor": {"cursor-1"}},
			{"page_size": {"100"}, "start_cursor": {"c+2/=?&"}},
		}
		records := requests()
		if len(records) != len(wantQueries) {
			t.Fatalf("requests = %d, want %d", len(records), len(wantQueries))
		}
		for i, record := range records {
			if record.method != http.MethodGet {
				t.Errorf("requests[%d].method = %q, want GET", i, record.method)
			}
			if record.path != "/v1/blocks/page-id/children" {
				t.Errorf("requests[%d].path = %q, want %q", i, record.path, "/v1/blocks/page-id/children")
			}
			if record.authorization != "Bearer test-token-value" {
				t.Errorf("requests[%d] Authorization = %q, want %q", i, record.authorization, "Bearer test-token-value")
			}
			if record.notionVersion != "2026-03-11" {
				t.Errorf("requests[%d] Notion-Version = %q, want %q", i, record.notionVersion, "2026-03-11")
			}
			if record.contentType != "" {
				t.Errorf("requests[%d] Content-Type = %q, want %q", i, record.contentType, "")
			}
			if record.bodyLen != 0 {
				t.Errorf("requests[%d] body length = %d, want 0", i, record.bodyLen)
			}
			gotQuery, err := url.ParseQuery(record.rawQuery)
			if err != nil {
				t.Fatalf("requests[%d].query %q does not parse: %v", i, record.rawQuery, err)
			}
			if !reflect.DeepEqual(gotQuery, wantQueries[i]) {
				t.Errorf("requests[%d].query = %#v, want %#v", i, gotQuery, wantQueries[i])
			}
		}
	})

	t.Run("rich_text の全要素の plain_text をつなぐ", func(t *testing.T) {
		client, _ := newBlocksTestClient(t, queryReply{body: `{"object":"list","type":"block","block":{},"results":[
			{"object":"block","id":"b-1","type":"quote","has_children":false,"quote":{"rich_text":[
				{"type":"text","text":{"content":"Deep "},"plain_text":"Deep "},
				{"type":"text","annotations":{"bold":true},"plain_text":"Work"},
				{"type":"mention","mention":{"type":"date"},"plain_text":" 2026"}
			],"color":"default"}},
			{"object":"block","id":"b-2","type":"paragraph","has_children":false,"paragraph":{"rich_text":[],"color":"default"}},
			{"object":"block","id":"b-3","type":"divider","has_children":false,"divider":{}},
			{"object":"block","id":"b-4","type":"child_page","has_children":false,"child_page":{"title":"Sub"}},
			{"object":"block","id":"b-5","type":"unsupported_kind","has_children":false}
		],"has_more":false,"next_cursor":null}`})

		got, err := client.ListBlockChildren(context.Background(), "page-id")
		if err != nil {
			t.Fatalf("ListBlockChildren() error = %v", err)
		}
		want := []Block{
			{ID: "b-1", Type: "quote", Text: "Deep Work 2026"},
			{ID: "b-2", Type: "paragraph", Text: ""},
			{ID: "b-3", Type: "divider", Text: ""},
			{ID: "b-4", Type: "child_page", Text: ""},
			{ID: "b-5", Type: "unsupported_kind", Text: ""},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ListBlockChildren() = %#v, want %#v", got, want)
		}
	})

	t.Run("0 件を取得する", func(t *testing.T) {
		client, _ := newBlocksTestClient(t, queryReply{body: lastChildrenPage})
		got, err := client.ListBlockChildren(context.Background(), "page-id")
		if err != nil {
			t.Fatalf("ListBlockChildren() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len(ListBlockChildren()) = %d, want 0", len(got))
		}
	})

	t.Run("ID を path に符号化する", func(t *testing.T) {
		client, requests := newBlocksTestClient(t, queryReply{body: lastChildrenPage})
		if _, err := client.ListBlockChildren(context.Background(), "a/b"); err != nil {
			t.Fatalf("ListBlockChildren() error = %v", err)
		}
		if got := requests()[0].path; got != "/v1/blocks/a%2Fb/children" {
			t.Errorf("path = %q, want %q", got, "/v1/blocks/a%2Fb/children")
		}
	})
}

func TestListBlockChildrenFailure(t *testing.T) {
	tests := []struct {
		name    string
		replies []queryReply
		want    string
	}{
		{
			name:    "1 回目で 404",
			replies: []queryReply{{status: http.StatusNotFound, body: `{"object":"error","status":404,"code":"object_not_found","message":"Could not find block."}`}},
			want:    "notion: GET /v1/blocks/page-id/children?page_size=100: HTTP 404 object_not_found: Could not find block.",
		},
		{
			name: "途中で 500",
			replies: []queryReply{
				{body: firstChildrenPage},
				{status: http.StatusInternalServerError, body: `{"object":"error","status":500,"code":"internal_server_error","message":"Unexpected error."}`},
			},
			want: "notion: GET /v1/blocks/page-id/children?page_size=100&start_cursor=cursor-1: HTTP 500 internal_server_error: Unexpected error.",
		},
		{
			name: "途中で 400",
			replies: []queryReply{
				{body: firstChildrenPage},
				{status: http.StatusBadRequest, body: `{"object":"error","status":400,"code":"bad_request","message":"Bad request."}`},
			},
			want: "notion: GET /v1/blocks/page-id/children?page_size=100&start_cursor=cursor-1: HTTP 400 bad_request: Bad request.",
		},
		{
			name: "3 回目で 503",
			replies: []queryReply{
				{body: firstChildrenPage},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-2"}`},
				{status: http.StatusServiceUnavailable, body: `{"object":"error","status":503,"code":"service_unavailable","message":"Unavailable."}`},
			},
			want: "notion: GET /v1/blocks/page-id/children?page_size=100&start_cursor=cursor-2: HTTP 503 service_unavailable: Unavailable.",
		},
		{
			name: "直前と同じ next_cursor",
			replies: []queryReply{
				{body: firstChildrenPage},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-1"}`},
			},
			want: "notion: GET /v1/blocks/page-id/children: invalid response: next_cursor was already used",
		},
		{
			name: "前に使った next_cursor",
			replies: []queryReply{
				{body: firstChildrenPage},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-2"}`},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-1"}`},
			},
			want: "notion: GET /v1/blocks/page-id/children: invalid response: next_cursor was already used",
		},
		{
			name: "途中で JSON ではない応答",
			replies: []queryReply{
				{body: firstChildrenPage},
				{body: `{"object":`},
			},
			want: "notion: GET /v1/blocks/page-id/children?page_size=100&start_cursor=cursor-1: invalid response JSON",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newBlocksTestClient(t, tt.replies...)
			got, err := client.ListBlockChildren(context.Background(), "page-id")
			assertNotionError(t, err)
			if got != nil {
				t.Errorf("ListBlockChildren() = %#v, want nil", got)
			}
			if msg := err.Error(); msg != tt.want {
				t.Errorf("error = %q, want %q", msg, tt.want)
			}
			if n := len(requests()); n != len(tt.replies) {
				t.Errorf("requests = %d, want %d", n, len(tt.replies))
			}
		})
	}
}

func TestListBlockChildrenInvalidResponse(t *testing.T) {
	const prefix = "notion: GET /v1/blocks/page-id/children: invalid response: "
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object がない", body: `{"results":[],"has_more":false,"next_cursor":null}`, want: "object is required"},
		{name: "object が異なる", body: `{"object":"block","results":[],"has_more":false,"next_cursor":null}`, want: "object must be list"},
		{name: "results がない", body: `{"object":"list","has_more":false,"next_cursor":null}`, want: "results is required"},
		{name: "results が null", body: `{"object":"list","results":null,"has_more":false,"next_cursor":null}`, want: "results is required"},
		{name: "has_more がない", body: `{"object":"list","results":[],"next_cursor":null}`, want: "has_more is required"},
		{name: "has_more が null", body: `{"object":"list","results":[],"has_more":null,"next_cursor":null}`, want: "has_more is required"},
		{name: "next_cursor がない", body: `{"object":"list","results":[],"has_more":false}`, want: "next_cursor is required"},
		{name: "next_cursor が文字列でない", body: `{"object":"list","results":[],"has_more":false,"next_cursor":1}`, want: "next_cursor must be a string or null"},
		{name: "has_more が true で next_cursor が null", body: `{"object":"list","results":[],"has_more":true,"next_cursor":null}`, want: "next_cursor must not be null when has_more is true"},
		{name: "has_more が true で next_cursor が空", body: `{"object":"list","results":[],"has_more":true,"next_cursor":""}`, want: "next_cursor must not be empty when has_more is true"},
		{name: "block がオブジェクトでない", body: `{"object":"list","results":["x"],"has_more":false,"next_cursor":null}`, want: "results[0] must be an object"},
		{name: "block の object がない", body: `{"object":"list","results":[{"id":"b","type":"paragraph"}],"has_more":false,"next_cursor":null}`, want: "results[0].object is required"},
		{name: "block の object が異なる", body: `{"object":"list","results":[{"object":"page","id":"b","type":"paragraph"}],"has_more":false,"next_cursor":null}`, want: "results[0].object must be block"},
		{name: "block の id がない", body: `{"object":"list","results":[{"object":"block","type":"paragraph"}],"has_more":false,"next_cursor":null}`, want: "results[0].id is required"},
		{name: "block の id が空", body: `{"object":"list","results":[{"object":"block","id":"","type":"paragraph"}],"has_more":false,"next_cursor":null}`, want: "results[0].id must not be empty"},
		{name: "block の type がない", body: `{"object":"list","results":[{"object":"block","id":"b"}],"has_more":false,"next_cursor":null}`, want: "results[0].type is required"},
		{name: "block の type が空", body: `{"object":"list","results":[{"object":"block","id":"b","type":""}],"has_more":false,"next_cursor":null}`, want: "results[0].type must not be empty"},
		{name: "2 件目の block が不正", body: `{"object":"list","results":[{"object":"block","id":"b","type":"paragraph"},{"object":"block","type":"paragraph"}],"has_more":false,"next_cursor":null}`, want: "results[1].id is required"},
		{name: "type のオブジェクトがオブジェクトでない", body: `{"object":"list","results":[{"object":"block","id":"b","type":"quote","quote":"x"}],"has_more":false,"next_cursor":null}`, want: "results[0].quote must be an object"},
		{name: "type のオブジェクトが null", body: `{"object":"list","results":[{"object":"block","id":"b","type":"quote","quote":null}],"has_more":false,"next_cursor":null}`, want: "results[0].quote must be an object"},
		{name: "rich_text が null", body: `{"object":"list","results":[{"object":"block","id":"b","type":"quote","quote":{"rich_text":null}}],"has_more":false,"next_cursor":null}`, want: "results[0].quote.rich_text is required"},
		{name: "rich_text が配列でない", body: `{"object":"list","results":[{"object":"block","id":"b","type":"quote","quote":{"rich_text":"x"}}],"has_more":false,"next_cursor":null}`, want: "results[0].quote.rich_text must be an array"},
		{name: "rich_text の plain_text がない", body: `{"object":"list","results":[{"object":"block","id":"b","type":"quote","quote":{"rich_text":[{"plain_text":"a"},{"type":"text"}]}}],"has_more":false,"next_cursor":null}`, want: "results[0].quote.rich_text[1].plain_text is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newBlocksTestClient(t, queryReply{body: firstChildrenPage}, queryReply{body: tt.body})
			got, err := client.ListBlockChildren(context.Background(), "page-id")
			assertNotionError(t, err)
			if got != nil {
				t.Errorf("ListBlockChildren() = %#v, want nil", got)
			}
			if msg := err.Error(); msg != prefix+tt.want {
				t.Errorf("error = %q, want %q", msg, prefix+tt.want)
			}
		})
	}
}

func TestListBlockChildrenRedactsToken(t *testing.T) {
	t.Run("ID の token を隠す", func(t *testing.T) {
		client, _ := newBlocksTestClient(t, queryReply{status: http.StatusNotFound, body: `{"code":"object_not_found","message":"Could not find block with ID: x."}`})
		_, err := client.ListBlockChildren(context.Background(), testToken)
		assertNotionError(t, err)
		const want = "notion: GET /v1/blocks/[REDACTED]/children?page_size=100: HTTP 404 object_not_found: Could not find block with ID: x."
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})

	t.Run("応答中の token を隠す", func(t *testing.T) {
		client, _ := newBlocksTestClient(t, queryReply{body: `{"object":"list","results":[{"object":"block","id":"b","type":"test-token-value","test-token-value":{"rich_text":null}}],"has_more":false,"next_cursor":null}`})
		_, err := client.ListBlockChildren(context.Background(), "page-id")
		assertNotionError(t, err)
		const want = "notion: GET /v1/blocks/page-id/children: invalid response: results[0].[REDACTED].rich_text is required"
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})
}
