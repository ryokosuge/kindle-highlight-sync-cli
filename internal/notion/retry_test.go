package notion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

const (
	rateLimited     = `{"object":"error","status":429,"code":"rate_limited","message":"Rate limited."}`
	serviceOverload = `{"object":"error","status":529,"code":"service_overload","message":"Overloaded."}`
)

type retryReply struct {
	status int
	header map[string]string
	body   string
}

type retryRequestRecord struct {
	method string
	path   string
	body   string
}

func newRetryTestClient(t *testing.T, replies ...retryReply) (*Client, func() []retryRequestRecord, *[]time.Duration, *bytes.Buffer) {
	t.Helper()
	var records []retryRequestRecord
	var waits []time.Duration
	var logs bytes.Buffer
	var mu sync.Mutex
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("要求本文の読み取りに失敗: %v", err)
		}
		mu.Lock()
		records = append(records, retryRequestRecord{
			method: r.Method,
			path:   r.URL.EscapedPath(),
			body:   string(body),
		})
		n := len(records)
		mu.Unlock()
		if n > len(replies) {
			t.Errorf("予期しない要求 #%d", n)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reply := replies[n-1]
		for key, value := range reply.header {
			w.Header().Set(key, value)
		}
		if reply.status != 0 {
			w.WriteHeader(reply.status)
		}
		_, _ = w.Write([]byte(reply.body))
	}, withRetrySleep(func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}), withRetryJitter(func() time.Duration {
		return 500 * time.Millisecond
	}), WithLogWriter(&logs))
	return client, func() []retryRequestRecord {
		mu.Lock()
		defer mu.Unlock()
		return append([]retryRequestRecord(nil), records...)
	}, &waits, &logs
}

func assertRetryWaits(t *testing.T, got *[]time.Duration, want []time.Duration) {
	t.Helper()
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("待機時間 = %v, want %v", *got, want)
	}
}

func assertRetryError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want Notion error")
	}
	if got := exitcode.Code(err); got != 4 {
		t.Errorf("exitcode.Code(err) = %d, want 4", got)
	}
	if got := err.Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		in   string
		secs int
		ok   bool
	}{
		{name: "5 秒", in: "5", secs: 5, ok: true},
		{name: "0 秒", in: "0", secs: 0, ok: true},
		{name: "空文字", in: "", ok: false},
		{name: "数値以外", in: "abc", ok: false},
		{name: "小数", in: "1.5", ok: false},
		{name: "負数", in: "-1", ok: false},
		{name: "HTTP 日付", in: "Wed, 21 Oct 2015 07:28:00 GMT", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSecs, gotOK := parseRetryAfter(tt.in)
			if gotSecs != tt.secs || gotOK != tt.ok {
				t.Errorf("parseRetryAfter(%q) = (%d, %t), want (%d, %t)", tt.in, gotSecs, gotOK, tt.secs, tt.ok)
			}
		})
	}
}

func TestWaitFor(t *testing.T) {
	policy := retryPolicy{jitter: func() time.Duration { return 500 * time.Millisecond }}
	tests := []struct {
		name       string
		n          int
		retryAfter time.Duration
		ok         bool
		want       time.Duration
	}{
		{name: "1 回目は Retry-After をそのまま使う", n: 1, retryAfter: 3 * time.Second, ok: true, want: 3 * time.Second},
		{name: "1 回目はバックオフにジッターを足す", n: 1, want: 1500 * time.Millisecond},
		{name: "2 回目は 2 秒のバックオフ", n: 2, want: 2500 * time.Millisecond},
		{name: "3 回目は 4 秒のバックオフ", n: 3, want: 4500 * time.Millisecond},
		{name: "5 回目は 16 秒のバックオフ", n: 5, want: 16500 * time.Millisecond},
		{name: "6 回目は 30 秒で上限", n: 6, want: 30500 * time.Millisecond},
		{name: "7 回目も 30 秒で上限", n: 7, want: 30500 * time.Millisecond},
		{name: "2 回目は大きい Retry-After を使う", n: 2, retryAfter: 10 * time.Second, ok: true, want: 10 * time.Second},
		{name: "3 回目はバックオフが Retry-After を超えたら使う", n: 3, retryAfter: time.Second, ok: true, want: 4500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.waitFor(tt.n, tt.retryAfter, tt.ok); got != tt.want {
				t.Errorf("waitFor(%d, %s, %t) = %s, want %s", tt.n, tt.retryAfter, tt.ok, got, tt.want)
			}
		})
	}
}

func TestRetryRecovery(t *testing.T) {
	t.Run("QueryDataSource を 429 から回復する", func(t *testing.T) {
		client, records, waits, _ := newRetryTestClient(t,
			retryReply{status: 429, header: map[string]string{"Retry-After": "2"}, body: rateLimited},
			retryReply{body: lastQueryPage},
		)
		got, err := client.QueryDataSource(context.Background(), "ds-1")
		if err != nil {
			t.Fatalf("QueryDataSource() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("QueryDataSource() = %#v, want no pages", got)
		}
		gotRecords := records()
		if len(gotRecords) != 2 {
			t.Fatalf("要求数 = %d, want 2", len(gotRecords))
		}
		for i, record := range gotRecords {
			if record.method != http.MethodPost || record.path != "/v1/data_sources/ds-1/query" {
				t.Errorf("要求[%d] = %s %s, want POST /v1/data_sources/ds-1/query", i, record.method, record.path)
			}
			if record.body == "" {
				t.Errorf("要求[%d]の本文が空", i)
			}
			if record.body != gotRecords[0].body {
				t.Errorf("要求[%d]の本文 = %q, 1 回目と異なる %q", i, record.body, gotRecords[0].body)
			}
		}
		assertRetryWaits(t, waits, []time.Duration{2 * time.Second})
	})

	t.Run("RetrieveDatabase を 529 から回復する", func(t *testing.T) {
		const database = `{"object":"database","id":"db-1","data_sources":[]}`
		client, records, waits, _ := newRetryTestClient(t,
			retryReply{status: 529, header: map[string]string{"Retry-After": "1"}, body: serviceOverload},
			retryReply{body: database},
		)
		got, err := client.RetrieveDatabase(context.Background(), "db-1")
		if err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		if got == nil || got.ID != "db-1" {
			t.Errorf("RetrieveDatabase() = %#v, want database db-1", got)
		}
		gotRecords := records()
		if len(gotRecords) != 2 {
			t.Fatalf("要求数 = %d, want 2", len(gotRecords))
		}
		first, second := gotRecords[0], gotRecords[1]
		if first.method != http.MethodGet || first.path != "/v1/databases/db-1" || first.body != "" {
			t.Errorf("1 回目の要求 = %#v, want GET /v1/databases/db-1 with no body", first)
		}
		if second.method != first.method || second.path != first.path || second.body != first.body {
			t.Errorf("2 回目の要求 = %#v, want method・path・body が 1 回目と同じ %#v", second, first)
		}
		assertRetryWaits(t, waits, []time.Duration{time.Second})
	})
}

func TestRetryAfterFallback(t *testing.T) {
	t.Run("ヘッダーなしと不正値ではバックオフを使う", func(t *testing.T) {
		const database = `{"object":"database","id":"db-1","data_sources":[]}`
		client, records, waits, _ := newRetryTestClient(t,
			retryReply{status: 429, body: rateLimited},
			retryReply{status: 529, header: map[string]string{"Retry-After": "abc"}, body: serviceOverload},
			retryReply{status: 429, header: map[string]string{"Retry-After": "1"}, body: rateLimited},
			retryReply{body: database},
		)
		if _, err := client.RetrieveDatabase(context.Background(), "db-1"); err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		if got := len(records()); got != 4 {
			t.Errorf("要求数 = %d, want 4", got)
		}
		assertRetryWaits(t, waits, []time.Duration{1500 * time.Millisecond, 2500 * time.Millisecond, 4500 * time.Millisecond})
	})

	t.Run("2 回目以降はバックオフと Retry-After の大きい方を使う", func(t *testing.T) {
		const database = `{"object":"database","id":"db-1","data_sources":[]}`
		client, records, waits, _ := newRetryTestClient(t,
			retryReply{status: 429, header: map[string]string{"Retry-After": "3"}, body: rateLimited},
			retryReply{status: 429, header: map[string]string{"Retry-After": "10"}, body: rateLimited},
			retryReply{body: database},
		)
		if _, err := client.RetrieveDatabase(context.Background(), "db-1"); err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		if got := len(records()); got != 3 {
			t.Errorf("要求数 = %d, want 3", got)
		}
		assertRetryWaits(t, waits, []time.Duration{3 * time.Second, 10 * time.Second})
	})
}

func TestRetryLimit(t *testing.T) {
	replies := []retryReply{
		{status: 429, body: rateLimited},
		{status: 429, body: rateLimited},
		{status: 429, body: rateLimited},
		{status: 429, body: rateLimited},
		{status: 429, body: rateLimited},
		{status: 529, body: serviceOverload},
	}
	client, records, waits, _ := newRetryTestClient(t, replies...)
	_, err := client.RetrieveDatabase(context.Background(), "db-1")
	assertRetryError(t, err, "notion: GET /v1/databases/db-1: retry limit exceeded after 5 retries: HTTP 529 service_overload: Overloaded.")
	if got := len(records()); got != 6 {
		t.Errorf("要求数 = %d, want 6", got)
	}
	if len(*waits) != 5 {
		t.Errorf("待機回数 = %d, want 5", len(*waits))
	}
}

func TestRetryAfterLimit(t *testing.T) {
	t.Run("上限を超える Retry-After では待たない", func(t *testing.T) {
		client, records, waits, _ := newRetryTestClient(t, retryReply{
			status: 429, header: map[string]string{"Retry-After": "61"}, body: rateLimited,
		})
		_, err := client.RetrieveDatabase(context.Background(), "db-1")
		assertRetryError(t, err, "notion: GET /v1/databases/db-1: Retry-After 61s exceeds 60s limit: HTTP 429 rate_limited: Rate limited.")
		if got := len(records()); got != 1 {
			t.Errorf("要求数 = %d, want 1", got)
		}
		assertRetryWaits(t, waits, nil)
	})

	t.Run("60 秒の Retry-After は待って再試行する", func(t *testing.T) {
		const database = `{"object":"database","id":"db-1","data_sources":[]}`
		client, records, waits, _ := newRetryTestClient(t,
			retryReply{status: 429, header: map[string]string{"Retry-After": "60"}, body: rateLimited},
			retryReply{body: database},
		)
		if _, err := client.RetrieveDatabase(context.Background(), "db-1"); err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		if got := len(records()); got != 2 {
			t.Errorf("要求数 = %d, want 2", got)
		}
		assertRetryWaits(t, waits, []time.Duration{60 * time.Second})
	})
}

func TestNoRetryForOtherStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		msg    string
	}{
		{name: "400 は再試行しない", status: 400, code: "bad_request", msg: "Bad request."},
		{name: "401 は再試行しない", status: 401, code: "unauthorized", msg: "Unauthorized."},
		{name: "404 は再試行しない", status: 404, code: "object_not_found", msg: "Not found."},
		{name: "500 は再試行しない", status: 500, code: "internal_server_error", msg: "Internal error."},
		{name: "503 は再試行しない", status: 503, code: "service_unavailable", msg: "Unavailable."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"object":"error","status":%d,"code":%q,"message":%q}`, tt.status, tt.code, tt.msg)
			client, records, waits, _ := newRetryTestClient(t, retryReply{
				status: tt.status, header: map[string]string{"Retry-After": "1"}, body: body,
			})
			_, err := client.RetrieveDatabase(context.Background(), "db-1")
			assertRetryError(t, err, fmt.Sprintf("notion: GET /v1/databases/db-1: HTTP %d %s: %s", tt.status, tt.code, tt.msg))
			if got := len(records()); got != 1 {
				t.Errorf("要求数 = %d, want 1", got)
			}
			assertRetryWaits(t, waits, nil)
		})
	}
}

func TestRetryCommunicationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := srv.URL
	srv.Close()
	var waits []time.Duration
	client := New(testToken, WithBaseURL(baseURL), withRetrySleep(func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}))
	_, err := client.RetrieveDatabase(context.Background(), "db-1")
	if err == nil {
		t.Fatal("RetrieveDatabase() error = nil, want communication error")
	}
	if got := exitcode.Code(err); got != 4 {
		t.Errorf("exitcode.Code(err) = %d, want 4", got)
	}
	if !strings.HasPrefix(err.Error(), "notion: GET /v1/databases/db-1: ") {
		t.Errorf("error = %q, want Notion error prefix", err)
	}
	if len(waits) != 0 {
		t.Errorf("待機時間 = %v, want none", waits)
	}
}

func TestRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, records, _, _ := newRetryTestClient(t, retryReply{
		status: 429, header: map[string]string{"Retry-After": "60"}, body: rateLimited,
	})
	client.retry.sleep = func(ctx context.Context, d time.Duration) error {
		timer := time.AfterFunc(10*time.Millisecond, cancel)
		defer timer.Stop()
		return sleepContext(ctx, d)
	}
	start := time.Now()
	_, err := client.RetrieveDatabase(ctx, "db-1")
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false, error = %v", err)
	}
	if got := exitcode.Code(err); got != 4 {
		t.Errorf("exitcode.Code(err) = %d, want 4", got)
	}
	if got := len(records()); got != 1 {
		t.Errorf("要求数 = %d, want 1", got)
	}
	if elapsed >= 5*time.Second {
		t.Errorf("取り消しまでの時間 = %s, want under 5s", elapsed)
	}
}

func TestRetryLog(t *testing.T) {
	client, _, waits, logs := newRetryTestClient(t,
		retryReply{status: 429, header: map[string]string{"Retry-After": "2"}, body: rateLimited},
		retryReply{status: 529, body: serviceOverload},
		retryReply{body: `{"object":"database","id":"db-1","data_sources":[]}`},
	)
	if _, err := client.RetrieveDatabase(context.Background(), testToken); err != nil {
		t.Fatalf("RetrieveDatabase() error = %v", err)
	}
	const want = "notion: HTTP 429: retry 1/5 after 2s\nnotion: HTTP 529: retry 2/5 after 2.5s\n"
	if got := logs.String(); got != want {
		t.Errorf("ログ = %q, want %q", got, want)
	}
	for _, secret := range []string{testToken, "Authorization", "Bearer", "/v1/", client.baseURL} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("ログに %q が含まれる: %q", secret, logs.String())
		}
	}
	assertRetryWaits(t, waits, []time.Duration{2 * time.Second, 2500 * time.Millisecond})
}
