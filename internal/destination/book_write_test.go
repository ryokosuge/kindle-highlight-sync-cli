package destination

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

const (
	testCreatedPageID   = "page-created"
	testCreatePagePath  = "/v1/pages"
	testAdoptPagePath   = "/v1/pages/page-asin"
	createdPageResponse = `{"object":"page","id":"page-created"}`
	adoptedPageResponse = `{"object":"page","id":"page-asin"}`
)

// writeMapping は last_annotated_at まで含めた全 Property の mapping。
var writeMapping = config.Mapping{
	Title:           titlePropertyID,
	KindleID:        kindleIDPropertyID,
	Author:          authorPropertyID,
	ASIN:            asinPropertyID,
	LastAnnotatedAt: lastAnnotatedAtPropertyID,
}

// annotatedAt は 2026-10-01 の 0 時 (UTC)。Kindle の Last Annotated Date は日付だけなので、書く値も日付だけになる。
var annotatedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

type notionRequest struct {
	line string // "METHOD path"
	body string
}

// newNotionTestServer は要求ごとに replies を順に返す Notion につないだ Client と、受けた要求（path と body）の記録を返す。
func newNotionTestServer(t *testing.T, replies ...notionReply) (*notion.Client, func() []notionRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []notionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		mu.Lock()
		requests = append(requests, notionRequest{line: r.Method + " " + r.URL.EscapedPath(), body: string(body)})
		n := len(requests)
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
	}))
	t.Cleanup(srv.Close)
	return notion.New("test-token-value", notion.WithBaseURL(srv.URL)), func() []notionRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]notionRequest(nil), requests...)
	}
}

func requestLines(requests []notionRequest) []string {
	lines := make([]string, 0, len(requests))
	for _, r := range requests {
		lines = append(lines, r.line)
	}
	return lines
}

func assertRequestLines(t *testing.T, got []notionRequest, want ...string) {
	t.Helper()
	if lines := requestLines(got); !slices.Equal(lines, want) {
		t.Errorf("requests = %v, want %v", lines, want)
	}
}

// wantRichText / wantTitle / wantDate は Notion client が送る Property の JSON を any にしたもの（pages_test.go の形）。
func wantRichText(text string) any {
	return map[string]any{"rich_text": []any{map[string]any{"type": "text", "text": map[string]any{"content": text}}}}
}

func wantTitle(text string) any {
	return map[string]any{"title": []any{map[string]any{"type": "text", "text": map[string]any{"content": text}}}}
}

func wantDate(start string) any {
	return map[string]any{"date": map[string]any{"start": start}}
}

// decodeProperties は要求の body から properties を取り出す。parent があれば data_source_id も確かめる。
func decodeProperties(t *testing.T, body string, wantDataSourceID string) map[string]any {
	t.Helper()
	var decoded struct {
		Parent     *map[string]any `json:"parent"`
		Properties map[string]any  `json:"properties"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(body) error = %v (body = %s)", err, body)
	}
	if wantDataSourceID != "" {
		wantParent := map[string]any{"type": "data_source_id", "data_source_id": wantDataSourceID}
		if decoded.Parent == nil || !reflect.DeepEqual(*decoded.Parent, wantParent) {
			t.Errorf("body.parent = %v, want %v", decoded.Parent, wantParent)
		}
	}
	return decoded.Properties
}

func newWriteDestination(t *testing.T, mapping config.Mapping, replies ...notionReply) (*NotionDestination, func() []notionRequest) {
	t.Helper()
	client, requests := newNotionTestServer(t, replies...)
	return NewNotionDestination(client, config.Notion{DataSourceID: testDataSourceID, Mapping: mapping}), requests
}

func TestNotionDestinationCreateBook(t *testing.T) {
	fullBook := kindleBook(testASIN, "Go", "Alan")
	fullBook.LastAnnotatedAt = &annotatedAt
	tests := []struct {
		name           string
		mapping        config.Mapping
		book           domain.Book
		wantProperties map[string]any
	}{
		{
			name:    "mapping のある Property に title / author / asin / Book ID / last_annotated_at を書く",
			mapping: writeMapping,
			book:    fullBook,
			wantProperties: map[string]any{
				titlePropertyID:           wantTitle("Go"),
				kindleIDPropertyID:        wantRichText(testASIN),
				authorPropertyID:          wantRichText("Alan"),
				asinPropertyID:            wantRichText(testASIN),
				lastAnnotatedAtPropertyID: wantDate("2026-10-01"),
			},
		},
		{
			name:    "author / asin / last_annotated_at の mapping が無ければ、値があっても body に入れない",
			mapping: config.Mapping{Title: titlePropertyID, KindleID: kindleIDPropertyID},
			book:    fullBook,
			wantProperties: map[string]any{
				titlePropertyID:    wantTitle("Go"),
				kindleIDPropertyID: wantRichText(testASIN),
			},
		},
		{
			name:    "author・ASIN・LastAnnotatedAt が空なら、mapping があっても body に入れず Kindle ID Property には b1- の Book ID を書く",
			mapping: writeMapping,
			book:    kindleBook("", "Go", ""),
			wantProperties: map[string]any{
				titlePropertyID:    wantTitle("Go"),
				kindleIDPropertyID: wantRichText(domain.NewBookID("", "Go", "")),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, requests := newWriteDestination(t, tt.mapping, queryReply(""), notionReply{body: createdPageResponse})

			got, err := d.CreateBook(context.Background(), tt.book)
			if err != nil {
				t.Fatalf("CreateBook() error = %v", err)
			}
			if want := (RemoteBook{ID: testCreatedPageID}); got != want {
				t.Errorf("CreateBook() = %+v, want %+v", got, want)
			}
			reqs := requests()
			assertRequestLines(t, reqs, "POST "+testQueryPath, "POST "+testCreatePagePath)
			if properties := decodeProperties(t, reqs[1].body, testDataSourceID); !reflect.DeepEqual(properties, tt.wantProperties) {
				t.Errorf("body.properties = %v, want %v", properties, tt.wantProperties)
			}
		})
	}
}

func TestNotionDestinationAdoptBook(t *testing.T) {
	book := kindleBook(testOtherASIN, "ASIN Page", "Alan")
	d, requests := newWriteDestination(t, writeMapping,
		queryReply("", bookPage("page-asin", "", testOtherASIN, "Other Title", "Other Author")),
		notionReply{body: adoptedPageResponse},
	)

	match, err := d.FindBook(context.Background(), book)
	if err != nil {
		t.Fatalf("FindBook() error = %v", err)
	}
	assertMatch(t, match, "adoptable", "page-asin")

	if err := d.AdoptBook(context.Background(), *match.Book, book); err != nil {
		t.Fatalf("AdoptBook() error = %v", err)
	}
	reqs := requests()
	assertRequestLines(t, reqs, "POST "+testQueryPath, "PATCH "+testAdoptPagePath)
	// Kindle ID Property にだけ Book ID を書き、title / author / asin / last_annotated_at は body に入れない
	wantProperties := map[string]any{kindleIDPropertyID: wantRichText(testOtherASIN)}
	if properties := decodeProperties(t, reqs[1].body, ""); !reflect.DeepEqual(properties, wantProperties) {
		t.Errorf("body.properties = %v, want %v", properties, wantProperties)
	}
}

// TestNotionDestinationWriteThenFindBook は書き込んだあと、同じ NotionDestination では Data Source を取り直さずに exact になり、
// 次の sync（新しい NotionDestination）では Data Source の応答に返るそのページで exact になることを確かめる。
func TestNotionDestinationWriteThenFindBook(t *testing.T) {
	createdBook := kindleBook(testASIN, "Created", "Alan")
	adoptedBook := kindleBook(testOtherASIN, "ASIN Page", "Alan")
	adoptablePage := bookPage("page-asin", "", testOtherASIN, "ASIN Page", "Alan")
	tests := []struct {
		name         string
		book         domain.Book
		reply        string                                          // 書き込みの要求への応答
		write        func(t *testing.T, d *NotionDestination) string // 書いたページの ID を返す
		wantRequests []string
	}{
		{
			name:  "CreateBook のあと",
			book:  createdBook,
			reply: createdPageResponse,
			write: func(t *testing.T, d *NotionDestination) string {
				t.Helper()
				remote, err := d.CreateBook(context.Background(), createdBook)
				if err != nil {
					t.Fatalf("CreateBook() error = %v", err)
				}
				return remote.ID
			},
			wantRequests: []string{"POST " + testQueryPath, "POST " + testCreatePagePath},
		},
		{
			name:  "AdoptBook のあと",
			book:  adoptedBook,
			reply: adoptedPageResponse,
			write: func(t *testing.T, d *NotionDestination) string {
				t.Helper()
				match, err := d.FindBook(context.Background(), adoptedBook)
				if err != nil {
					t.Fatalf("FindBook() error = %v", err)
				}
				assertMatch(t, match, "adoptable", "page-asin")
				if err := d.AdoptBook(context.Background(), *match.Book, adoptedBook); err != nil {
					t.Fatalf("AdoptBook() error = %v", err)
				}
				return match.Book.ID
			},
			wantRequests: []string{"POST " + testQueryPath, "PATCH " + testAdoptPagePath},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, requests := newWriteDestination(t, writeMapping, queryReply("", adoptablePage), notionReply{body: tt.reply})
			pageID := tt.write(t, d)

			// 同じ NotionDestination: Data Source を取り直さずに exact
			got, err := d.FindBook(context.Background(), tt.book)
			if err != nil {
				t.Fatalf("FindBook() after write error = %v", err)
			}
			assertMatch(t, got, "exact", pageID)
			assertRequestLines(t, requests(), tt.wantRequests...)

			// 次の sync: 書いたページが Data Source の応答に返る状態で、新しい NotionDestination でも exact
			next, nextRequests := newWriteDestination(t, writeMapping,
				queryReply("", bookPage(pageID, tt.book.ID, tt.book.ASIN, tt.book.Title, tt.book.Author)),
			)
			got, err = next.FindBook(context.Background(), tt.book)
			if err != nil {
				t.Fatalf("FindBook() on next sync error = %v", err)
			}
			assertMatch(t, got, "exact", pageID)
			assertRequestLines(t, nextRequests(), "POST "+testQueryPath)
		})
	}
}

// TestNotionDestinationWriteBeforeFindBook は FindBook より先に書き込みが呼ばれても、index を作ってから反映することを確かめる。
func TestNotionDestinationWriteBeforeFindBook(t *testing.T) {
	book := kindleBook(testOtherASIN, "ASIN Page", "Alan")
	adoptablePage := bookPage("page-asin", "", testOtherASIN, "ASIN Page", "Alan")
	tests := []struct {
		name         string
		reply        string
		write        func(t *testing.T, d *NotionDestination)
		wantID       string
		wantRequests []string
	}{
		{
			name:  "CreateBook が先でも、index を作ってから作成したページを足す",
			reply: createdPageResponse,
			write: func(t *testing.T, d *NotionDestination) {
				t.Helper()
				if _, err := d.CreateBook(context.Background(), book); err != nil {
					t.Fatalf("CreateBook() error = %v", err)
				}
			},
			wantID:       testCreatedPageID,
			wantRequests: []string{"POST " + testQueryPath, "POST " + testCreatePagePath},
		},
		{
			name:  "AdoptBook が先でも、index を作ってから Adopt したページを反映する",
			reply: adoptedPageResponse,
			write: func(t *testing.T, d *NotionDestination) {
				t.Helper()
				if err := d.AdoptBook(context.Background(), RemoteBook{ID: "page-asin"}, book); err != nil {
					t.Fatalf("AdoptBook() error = %v", err)
				}
			},
			wantID:       "page-asin",
			wantRequests: []string{"POST " + testQueryPath, "PATCH " + testAdoptPagePath},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, requests := newWriteDestination(t, writeMapping, queryReply("", adoptablePage), notionReply{body: tt.reply})
			tt.write(t, d)

			got, err := d.FindBook(context.Background(), book)
			if err != nil {
				t.Fatalf("FindBook() error = %v", err)
			}
			assertMatch(t, got, "exact", tt.wantID)
			assertRequestLines(t, requests(), tt.wantRequests...)
		})
	}
}

func TestNotionDestinationWriteFailure(t *testing.T) {
	book := kindleBook(testOtherASIN, "ASIN Page", "Alan")
	adoptablePage := bookPage("page-asin", "", testOtherASIN, "ASIN Page", "Alan")
	failure := notionReply{status: http.StatusBadRequest, body: notionErrorBody}
	create := func(d *NotionDestination) error {
		_, err := d.CreateBook(context.Background(), book)
		return err
	}
	adopt := func(d *NotionDestination) error {
		return d.AdoptBook(context.Background(), RemoteBook{ID: "page-asin"}, book)
	}
	tests := []struct {
		name         string
		replies      []notionReply
		write        func(d *NotionDestination) error
		wantRequests []string // 失敗した呼び出しと、そのあとの FindBook の要求
	}{
		{
			name:         "CreateBook で index の全件取得に失敗したらページを作らず、次の FindBook で取り直す",
			replies:      []notionReply{failure, queryReply("", adoptablePage)},
			write:        create,
			wantRequests: []string{"POST " + testQueryPath, "POST " + testQueryPath},
		},
		{
			name:         "CreateBook でページの作成に失敗したら index を変えない",
			replies:      []notionReply{queryReply("", adoptablePage), failure},
			write:        create,
			wantRequests: []string{"POST " + testQueryPath, "POST " + testCreatePagePath},
		},
		{
			name:         "AdoptBook で index の全件取得に失敗したらページを更新せず、次の FindBook で取り直す",
			replies:      []notionReply{failure, queryReply("", adoptablePage)},
			write:        adopt,
			wantRequests: []string{"POST " + testQueryPath, "POST " + testQueryPath},
		},
		{
			name:         "AdoptBook でページの更新に失敗したら index を変えない",
			replies:      []notionReply{queryReply("", adoptablePage), failure},
			write:        adopt,
			wantRequests: []string{"POST " + testQueryPath, "PATCH " + testAdoptPagePath},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, requests := newWriteDestination(t, writeMapping, tt.replies...)

			err := tt.write(d)
			if got := exitcode.Code(err); got != 4 {
				t.Fatalf("exitcode.Code(err) = %d, want 4 (err = %v)", got, err)
			}
			var status *notion.StatusError
			if !errors.As(err, &status) || status.Status != http.StatusBadRequest {
				t.Errorf("StatusError = %+v, want Status 400", status)
			}

			// 書けていないので、そのページは Adopt の候補のまま
			got, err := d.FindBook(context.Background(), book)
			if err != nil {
				t.Fatalf("FindBook() after failure error = %v", err)
			}
			assertMatch(t, got, "adoptable", "page-asin")
			assertRequestLines(t, requests(), tt.wantRequests...)
		})
	}
}
