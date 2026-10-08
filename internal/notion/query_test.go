package notion

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
)

type queryReply struct {
	status int
	body   string
}

type queryRequestRecord struct {
	method, path, authorization, notionVersion, contentType string
	body                                                    map[string]any
}

// newQueryTestClient は要求ごとに replies を順に返すサーバーにつないだ Client と、受けた要求の記録を返す。
func newQueryTestClient(t *testing.T, replies ...queryReply) (*Client, func() []queryRequestRecord) {
	t.Helper()
	var mu sync.Mutex
	var records []queryRequestRecord
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		record := queryRequestRecord{
			method:        r.Method,
			path:          r.URL.EscapedPath(),
			authorization: r.Header.Get("Authorization"),
			notionVersion: r.Header.Get("Notion-Version"),
			contentType:   r.Header.Get("Content-Type"),
		}
		if err := json.Unmarshal(raw, &record.body); err != nil {
			t.Errorf("request body %q is not JSON: %v", raw, err)
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
	return client, func() []queryRequestRecord {
		mu.Lock()
		defer mu.Unlock()
		return append([]queryRequestRecord(nil), records...)
	}
}

const (
	firstQueryPage = `{"object":"list","type":"page_or_data_source","page_or_data_source":{},
		"results":[{"object":"page","id":"page-1","properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"Book 1"}]}}}],
		"has_more":true,"next_cursor":"cursor-1"}`
	lastQueryPage = `{"object":"list","results":[],"has_more":false,"next_cursor":null}`
)

func TestQueryDataSource(t *testing.T) {
	t.Run("next_cursor を辿って最後のページまで返す", func(t *testing.T) {
		client, requests := newQueryTestClient(t,
			queryReply{body: firstQueryPage},
			queryReply{body: `{"object":"list","results":[
				{"object":"page","id":"page-2","properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"Book 2"}]}}},
				{"object":"page","id":"page-3","properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"Book 3"}]}}}
			],"has_more":true,"next_cursor":"cursor-2","request_status":{"type":"complete"}}`},
			queryReply{body: `{"object":"list","results":[
				{"object":"page","id":"page-4","properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"Book 4"}]}}}
			],"has_more":false,"next_cursor":null,"request_status":{"type":"complete"}}`},
		)

		got, err := client.QueryDataSource(context.Background(), "ds")
		if err != nil {
			t.Fatalf("QueryDataSource() error = %v", err)
		}
		want := []Page{
			{ID: "page-1", Properties: map[string]PropertyValue{"title": {Type: "title", Text: "Book 1"}}},
			{ID: "page-2", Properties: map[string]PropertyValue{"title": {Type: "title", Text: "Book 2"}}},
			{ID: "page-3", Properties: map[string]PropertyValue{"title": {Type: "title", Text: "Book 3"}}},
			{ID: "page-4", Properties: map[string]PropertyValue{"title": {Type: "title", Text: "Book 4"}}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("QueryDataSource() = %#v, want %#v", got, want)
		}

		wantBodies := []map[string]any{
			{"page_size": float64(100), "result_type": "page"},
			{"page_size": float64(100), "result_type": "page", "start_cursor": "cursor-1"},
			{"page_size": float64(100), "result_type": "page", "start_cursor": "cursor-2"},
		}
		records := requests()
		if len(records) != len(wantBodies) {
			t.Fatalf("requests = %d, want %d", len(records), len(wantBodies))
		}
		for i, record := range records {
			if record.method != http.MethodPost {
				t.Errorf("requests[%d].method = %q, want POST", i, record.method)
			}
			if record.path != "/v1/data_sources/ds/query" {
				t.Errorf("requests[%d].path = %q, want %q", i, record.path, "/v1/data_sources/ds/query")
			}
			if record.authorization != "Bearer test-token-value" {
				t.Errorf("requests[%d] Authorization = %q, want %q", i, record.authorization, "Bearer test-token-value")
			}
			if record.notionVersion != "2026-03-11" {
				t.Errorf("requests[%d] Notion-Version = %q, want %q", i, record.notionVersion, "2026-03-11")
			}
			if record.contentType != "application/json" {
				t.Errorf("requests[%d] Content-Type = %q, want %q", i, record.contentType, "application/json")
			}
			if !reflect.DeepEqual(record.body, wantBodies[i]) {
				t.Errorf("requests[%d].body = %#v, want %#v", i, record.body, wantBodies[i])
			}
		}
	})

	t.Run("Property の値を Property ID で引ける", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{body: `{"object":"list","results":[{
			"object":"page","id":"page-1","in_trash":false,
			"properties":{
				"Name":{"id":"title","type":"title","title":[
					{"type":"text","text":{"content":"Deep "},"plain_text":"Deep "},
					{"type":"text","annotations":{"bold":true},"plain_text":"Work"},
					{"type":"mention","mention":{"type":"date"},"plain_text":" 2026"}
				]},
				"Author":{"id":"%3Axs","type":"rich_text","rich_text":[
					{"type":"text","plain_text":"Cal "},
					{"type":"text","plain_text":"Newport"}
				]},
				"Kindle ID":{"id":"k%40id","type":"rich_text","rich_text":[]},
				"Last Annotated":{"id":"Ef56","type":"date","date":{"start":"2026-09-15T09:00:00.000+09:00","end":null,"time_zone":null}},
				"Empty Date":{"id":"ed","type":"date","date":null},
				"Done":{"id":"cb","type":"checkbox","checkbox":true}
			}
		}],"has_more":false,"next_cursor":null}`})

		got, err := client.QueryDataSource(context.Background(), "ds")
		if err != nil {
			t.Fatalf("QueryDataSource() error = %v", err)
		}
		want := []Page{{
			ID: "page-1",
			Properties: map[string]PropertyValue{
				"title":  {Type: "title", Text: "Deep Work 2026"},
				"%3Axs":  {Type: "rich_text", Text: "Cal Newport"},
				"k%40id": {Type: "rich_text", Text: ""},
				"Ef56":   {Type: "date", Date: "2026-09-15T09:00:00.000+09:00"},
				"ed":     {Type: "date", Date: ""},
				"cb":     {Type: "checkbox"},
			},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("QueryDataSource() = %#v, want %#v", got, want)
		}
	})

	t.Run("0 件を取得する", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{body: lastQueryPage})
		got, err := client.QueryDataSource(context.Background(), "ds")
		if err != nil {
			t.Fatalf("QueryDataSource() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len(QueryDataSource()) = %d, want 0", len(got))
		}
	})

	t.Run("ID を path に符号化する", func(t *testing.T) {
		client, requests := newQueryTestClient(t, queryReply{body: lastQueryPage})
		if _, err := client.QueryDataSource(context.Background(), "a/b"); err != nil {
			t.Fatalf("QueryDataSource() error = %v", err)
		}
		if got := requests()[0].path; got != "/v1/data_sources/a%2Fb/query" {
			t.Errorf("path = %q, want %q", got, "/v1/data_sources/a%2Fb/query")
		}
	})
}

func TestQueryDataSourceFailure(t *testing.T) {
	tests := []struct {
		name    string
		replies []queryReply
		want    string
	}{
		{
			name:    "1 回目で incomplete",
			replies: []queryReply{{body: `{"object":"list","results":[],"has_more":false,"next_cursor":null,"request_status":{"type":"incomplete","incomplete_reason":"query_result_limit_reached"}}`}},
			want:    "notion: POST /v1/data_sources/ds/query: query result is incomplete: query_result_limit_reached",
		},
		{
			name: "途中で incomplete",
			replies: []queryReply{
				{body: firstQueryPage},
				{body: `{"object":"list","results":[{"object":"page","id":"page-2","properties":{}}],"has_more":false,"next_cursor":null,"request_status":{"type":"incomplete","incomplete_reason":"query_result_limit_reached"}}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: query result is incomplete: query_result_limit_reached",
		},
		{
			name: "incomplete_reason のない incomplete",
			replies: []queryReply{
				{body: firstQueryPage},
				{body: `{"object":"list","results":[],"has_more":false,"next_cursor":null,"request_status":{"type":"incomplete"}}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: query result is incomplete",
		},
		{
			name:    "1 回目で 2xx 以外",
			replies: []queryReply{{status: http.StatusNotFound, body: `{"object":"error","status":404,"code":"object_not_found","message":"Could not find data source."}`}},
			want:    "notion: POST /v1/data_sources/ds/query: HTTP 404 object_not_found: Could not find data source.",
		},
		{
			name: "途中で 500",
			replies: []queryReply{
				{body: firstQueryPage},
				{status: http.StatusInternalServerError, body: `{"object":"error","status":500,"code":"internal_server_error","message":"Unexpected error."}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: HTTP 500 internal_server_error: Unexpected error.",
		},
		{
			name: "途中で 429",
			replies: []queryReply{
				{body: firstQueryPage},
				{status: http.StatusTooManyRequests, body: `{"object":"error","status":429,"code":"rate_limited","message":"Rate limited."}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: HTTP 429 rate_limited: Rate limited.",
		},
		{
			name: "3 回目で 2xx 以外",
			replies: []queryReply{
				{body: firstQueryPage},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-2"}`},
				{status: http.StatusServiceUnavailable, body: `{"object":"error","status":503,"code":"service_unavailable","message":"Unavailable."}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: HTTP 503 service_unavailable: Unavailable.",
		},
		{
			name: "直前と同じ next_cursor",
			replies: []queryReply{
				{body: firstQueryPage},
				{body: `{"object":"list","results":[{"object":"page","id":"page-2","properties":{}}],"has_more":true,"next_cursor":"cursor-1"}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: invalid response: next_cursor was already used",
		},
		{
			name: "前に使った next_cursor",
			replies: []queryReply{
				{body: firstQueryPage},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-2"}`},
				{body: `{"object":"list","results":[],"has_more":true,"next_cursor":"cursor-1"}`},
			},
			want: "notion: POST /v1/data_sources/ds/query: invalid response: next_cursor was already used",
		},
		{
			name: "途中で JSON ではない応答",
			replies: []queryReply{
				{body: firstQueryPage},
				{body: `{"object":`},
			},
			want: "notion: POST /v1/data_sources/ds/query: invalid response JSON",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newQueryTestClient(t, tt.replies...)
			got, err := client.QueryDataSource(context.Background(), "ds")
			assertNotionError(t, err)
			if got != nil {
				t.Errorf("QueryDataSource() = %#v, want nil", got)
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

func TestQueryDataSourceInvalidResponse(t *testing.T) {
	const prefix = "notion: POST /v1/data_sources/ds/query: invalid response: "
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object がない", body: `{"results":[],"has_more":false,"next_cursor":null}`, want: "object is required"},
		{name: "object が異なる", body: `{"object":"page","results":[],"has_more":false,"next_cursor":null}`, want: "object must be list"},
		{name: "results がない", body: `{"object":"list","has_more":false,"next_cursor":null}`, want: "results is required"},
		{name: "results が null", body: `{"object":"list","results":null,"has_more":false,"next_cursor":null}`, want: "results is required"},
		{name: "has_more がない", body: `{"object":"list","results":[],"next_cursor":null}`, want: "has_more is required"},
		{name: "has_more が null", body: `{"object":"list","results":[],"has_more":null,"next_cursor":null}`, want: "has_more is required"},
		{name: "next_cursor がない", body: `{"object":"list","results":[],"has_more":false}`, want: "next_cursor is required"},
		{name: "next_cursor が文字列でない", body: `{"object":"list","results":[],"has_more":false,"next_cursor":1}`, want: "next_cursor must be a string or null"},
		{name: "has_more が true で next_cursor が null", body: `{"object":"list","results":[],"has_more":true,"next_cursor":null}`, want: "next_cursor must not be null when has_more is true"},
		{name: "has_more が true で next_cursor が空", body: `{"object":"list","results":[],"has_more":true,"next_cursor":""}`, want: "next_cursor must not be empty when has_more is true"},
		{name: "request_status.type がない", body: `{"object":"list","results":[],"has_more":false,"next_cursor":null,"request_status":{}}`, want: "request_status.type is required"},
		{name: "request_status.type が未知", body: `{"object":"list","results":[],"has_more":false,"next_cursor":null,"request_status":{"type":"partial"}}`, want: "request_status.type must be complete or incomplete"},
		{name: "page の object がない", body: `{"object":"list","results":[{"id":"p","properties":{}}],"has_more":false,"next_cursor":null}`, want: "results[0].object is required"},
		{name: "page の object が異なる", body: `{"object":"list","results":[{"object":"data_source","id":"p","properties":{}}],"has_more":false,"next_cursor":null}`, want: "results[0].object must be page"},
		{name: "page の id がない", body: `{"object":"list","results":[{"object":"page","properties":{}}],"has_more":false,"next_cursor":null}`, want: "results[0].id is required"},
		{name: "page の id が空", body: `{"object":"list","results":[{"object":"page","id":"","properties":{}}],"has_more":false,"next_cursor":null}`, want: "results[0].id must not be empty"},
		{name: "page の properties がない", body: `{"object":"list","results":[{"object":"page","id":"p"}],"has_more":false,"next_cursor":null}`, want: "results[0].properties is required"},
		{name: "page の properties が null", body: `{"object":"list","results":[{"object":"page","id":"p","properties":null}],"has_more":false,"next_cursor":null}`, want: "results[0].properties is required"},
		{name: "2 件目の page が不正", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{}},{"object":"page","properties":{}}],"has_more":false,"next_cursor":null}`, want: "results[1].id is required"},
		{name: "Property の id がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Done":{"type":"checkbox"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Done"].id is required`},
		{name: "Property の id が空", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Done":{"id":"","type":"checkbox"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Done"].id must not be empty`},
		{name: "Property の id が重複する", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"A":{"id":"same","type":"checkbox"},"B":{"id":"same","type":"checkbox"}}}],"has_more":false,"next_cursor":null}`, want: "results[0].properties contains duplicate id"},
		{name: "Property の type がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Done":{"id":"cb"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Done"].type is required`},
		{name: "Property の type が空", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Done":{"id":"cb","type":""}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Done"].type must not be empty`},
		{name: "title がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Name":{"id":"title","type":"title"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Name"].title is required`},
		{name: "title が null", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Name":{"id":"title","type":"title","title":null}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Name"].title is required`},
		{name: "title の plain_text がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Name":{"id":"title","type":"title","title":[{"plain_text":"a"},{"type":"text"}]}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Name"].title[1].plain_text is required`},
		{name: "rich_text がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Author":{"id":"a","type":"rich_text"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Author"].rich_text is required`},
		{name: "rich_text の plain_text が null", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"Author":{"id":"a","type":"rich_text","rich_text":[{"plain_text":null}]}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["Author"].rich_text[0].plain_text is required`},
		{name: "date がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"At":{"id":"d","type":"date"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["At"].date is required`},
		{name: "date がオブジェクトでない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"At":{"id":"d","type":"date","date":"2026-09-15"}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["At"].date must be an object or null`},
		{name: "date の start がない", body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"At":{"id":"d","type":"date","date":{"end":null}}}}],"has_more":false,"next_cursor":null}`, want: `results[0].properties["At"].date.start is required`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newQueryTestClient(t, queryReply{body: firstQueryPage}, queryReply{body: tt.body})
			got, err := client.QueryDataSource(context.Background(), "ds")
			assertNotionError(t, err)
			if got != nil {
				t.Errorf("QueryDataSource() = %#v, want nil", got)
			}
			if msg := err.Error(); msg != prefix+tt.want {
				t.Errorf("error = %q, want %q", msg, prefix+tt.want)
			}
		})
	}
}

func TestQueryDataSourceRedactsToken(t *testing.T) {
	t.Run("ID の token を隠す", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{status: http.StatusNotFound, body: `{"code":"object_not_found","message":"Could not find data source."}`})
		_, err := client.QueryDataSource(context.Background(), testToken)
		assertNotionError(t, err)
		const want = "notion: POST /v1/data_sources/[REDACTED]/query: HTTP 404 object_not_found: Could not find data source."
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})

	t.Run("応答中の token を隠す", func(t *testing.T) {
		client, _ := newQueryTestClient(t, queryReply{body: `{"object":"list","results":[{"object":"page","id":"p","properties":{"test-token-value":{"id":"x"}}}],"has_more":false,"next_cursor":null}`})
		_, err := client.QueryDataSource(context.Background(), "ds")
		assertNotionError(t, err)
		const want = `notion: POST /v1/data_sources/ds/query: invalid response: results[0].properties["[REDACTED]"].type is required`
		if got := err.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	})
}
