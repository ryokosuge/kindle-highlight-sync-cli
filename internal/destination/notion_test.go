package destination

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

const (
	testDataSourceID   = "ds-1"
	testQueryPath      = "/v1/data_sources/ds-1/query"
	titlePropertyID    = "title"
	kindleIDPropertyID = "kid%3A"
	authorPropertyID   = "aut%3B"
	asinPropertyID     = "asn%3C"
	notionErrorBody    = `{"object":"error","status":400,"code":"validation_error","message":"bad request"}`
)

var fullMapping = config.Mapping{
	Title:    titlePropertyID,
	KindleID: kindleIDPropertyID,
	Author:   authorPropertyID,
	ASIN:     asinPropertyID,
}

type notionReply struct {
	status int
	body   string
}

// newNotionTestClient は要求ごとに replies を順に返す Notion につないだ Client と、受けた要求の path の記録を返す。
func newNotionTestClient(t *testing.T, replies ...notionReply) (*notion.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		n := len(paths)
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
	return notion.New("test-token-value", notion.WithBaseURL(srv.URL)), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// property は Property 名を key にした page の Property の JSON。typ は "title" か "rich_text"。
func property(name, id, typ, text string) string {
	return fmt.Sprintf(`%q:{"id":%q,"type":%q,%q:[{"plain_text":%q}]}`, name, id, typ, typ, text)
}

func pageJSON(id string, properties ...string) string {
	return fmt.Sprintf(`{"object":"page","id":%q,"properties":{%s}}`, id, strings.Join(properties, ","))
}

// queryReply は 1 回分の Query a data source の応答。nextCursor が空なら最後のページ。
func queryReply(nextCursor string, pages ...string) notionReply {
	cursor, hasMore := "null", false
	if nextCursor != "" {
		cursor, hasMore = fmt.Sprintf("%q", nextCursor), true
	}
	return notionReply{body: fmt.Sprintf(`{"object":"list","results":[%s],"has_more":%t,"next_cursor":%s}`, strings.Join(pages, ","), hasMore, cursor)}
}

func bookPage(id, kindleID, asin, title, author string) string {
	return pageJSON(id,
		property("Name", titlePropertyID, "title", title),
		property("Kindle ID", kindleIDPropertyID, "rich_text", kindleID),
		property("Author", authorPropertyID, "rich_text", author),
		property("ASIN", asinPropertyID, "rich_text", asin),
	)
}

func assertMatch(t *testing.T, got BookMatch, wantStatus BookMatchStatus, wantID string) {
	t.Helper()
	if got.Status != wantStatus {
		t.Errorf("Status = %q, want %q", got.Status, wantStatus)
	}
	if wantID == "" {
		if got.Book != nil {
			t.Errorf("Book = %+v, want nil", *got.Book)
		}
		return
	}
	if got.Book == nil {
		t.Fatalf("Book = nil, want ID %q", wantID)
	}
	if got.Book.ID != wantID {
		t.Errorf("Book.ID = %q, want %q", got.Book.ID, wantID)
	}
}

func assertRequests(t *testing.T, got []string, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("requests = %v, want %d requests", got, want)
	}
	for i, r := range got {
		if r != "POST "+testQueryPath {
			t.Errorf("requests[%d] = %q, want %q", i, r, "POST "+testQueryPath)
		}
	}
}

func TestNotionDestinationFindBook(t *testing.T) {
	client, requests := newNotionTestClient(t,
		queryReply("cursor-1",
			bookPage("page-exact", testASIN, "", "Kindle ID Page", "Alan"),
			bookPage("page-asin", "", testOtherASIN, "ASIN Page", "Alan"),
		),
		queryReply("",
			// Database 側で Kindle ID Property と Author が消されたページ
			pageJSON("page-title", property("Name", titlePropertyID, "title", "Title Only")),
			bookPage("page-duplicate-1", "", "", "Duplicate", ""),
			bookPage("page-duplicate-2", "", "", "Duplicate", ""),
		),
	)
	d := NewNotionDestination(client, config.Notion{DataSourceID: testDataSourceID, Mapping: fullMapping})

	tests := []struct {
		name       string
		book       domain.Book
		wantStatus BookMatchStatus
		wantID     string
	}{
		{
			name:       "Kindle ID Property が一致すれば exact",
			book:       kindleBook(testASIN, "Other", "Other"),
			wantStatus: "exact",
			wantID:     "page-exact",
		},
		{
			name:       "ASIN が一致すれば adoptable",
			book:       kindleBook(testOtherASIN, "Other", "Other"),
			wantStatus: "adoptable",
			wantID:     "page-asin",
		},
		{
			name:       "Title だけが一致すれば adoptable",
			book:       kindleBook("", "Title Only", "Bob"),
			wantStatus: "adoptable",
			wantID:     "page-title",
		},
		{
			name:       "該当が無ければ none",
			book:       kindleBook("", "Missing", "Bob"),
			wantStatus: "none",
		},
		{
			name:       "同じ Title のページが 2 件なら ambiguous",
			book:       kindleBook("", "Duplicate", "Bob"),
			wantStatus: "ambiguous",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.FindBook(context.Background(), tt.book)
			if err != nil {
				t.Fatalf("FindBook() error = %v", err)
			}
			assertMatch(t, got, tt.wantStatus, tt.wantID)
		})
	}
	assertRequests(t, requests(), 2)
}

func TestNotionDestinationFindBookMapping(t *testing.T) {
	pages := queryReply("",
		bookPage("page-asin", "", testOtherASIN, "ASIN Page", ""),
		bookPage("page-go-bob", "", "", "Go", "Bob"),
		bookPage("page-go-no-author", "", "", "Go", ""),
	)
	tests := []struct {
		name       string
		mapping    config.Mapping
		book       domain.Book
		wantStatus BookMatchStatus
		wantID     string
	}{
		{
			name:       "asin の対応があれば ASIN Property で照合する",
			mapping:    fullMapping,
			book:       kindleBook(testOtherASIN, "Other", ""),
			wantStatus: "adoptable",
			wantID:     "page-asin",
		},
		{
			name:       "asin の対応が無ければ ASIN Property を照合に使わない",
			mapping:    config.Mapping{Title: titlePropertyID, KindleID: kindleIDPropertyID, Author: authorPropertyID},
			book:       kindleBook(testOtherASIN, "Other", ""),
			wantStatus: "none",
		},
		{
			name:       "author の対応があれば Author Property で Title+Author を照合する",
			mapping:    fullMapping,
			book:       kindleBook("", "Go", "Bob"),
			wantStatus: "adoptable",
			wantID:     "page-go-bob",
		},
		{
			name:       "author の対応があれば author が両方あって違うページは Title だけの段の候補に数えない",
			mapping:    fullMapping,
			book:       kindleBook("", "Go", "Alan"),
			wantStatus: "adoptable",
			wantID:     "page-go-no-author",
		},
		{
			name:       "author の対応が無ければ Author Property を使わず、同じ Title のページ 2 件は ambiguous",
			mapping:    config.Mapping{Title: titlePropertyID, KindleID: kindleIDPropertyID, ASIN: asinPropertyID},
			book:       kindleBook("", "Go", "Bob"),
			wantStatus: "ambiguous",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := newNotionTestClient(t, pages)
			d := NewNotionDestination(client, config.Notion{DataSourceID: testDataSourceID, Mapping: tt.mapping})
			got, err := d.FindBook(context.Background(), tt.book)
			if err != nil {
				t.Fatalf("FindBook() error = %v", err)
			}
			assertMatch(t, got, tt.wantStatus, tt.wantID)
			assertRequests(t, requests(), 1)
		})
	}
}

func TestNotionDestinationFindBookFailure(t *testing.T) {
	book := kindleBook(testASIN, "Go", "Alan")
	firstPage := queryReply("cursor-1", bookPage("page-exact", testASIN, "", "Go", "Alan"))
	tests := []struct {
		name    string
		replies []notionReply
	}{
		{
			name:    "最初の要求が失敗する",
			replies: []notionReply{{status: http.StatusBadRequest, body: notionErrorBody}},
		},
		{
			name:    "途中のページの要求が失敗する",
			replies: []notionReply{firstPage, {status: http.StatusBadRequest, body: notionErrorBody}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 失敗のあとの呼び出しでは全件を取り直す
			replies := append(append([]notionReply(nil), tt.replies...), firstPage, queryReply(""))
			client, requests := newNotionTestClient(t, replies...)
			d := NewNotionDestination(client, config.Notion{DataSourceID: testDataSourceID, Mapping: fullMapping})

			got, err := d.FindBook(context.Background(), book)
			if got := exitcode.Code(err); got != 4 {
				t.Fatalf("exitcode.Code(err) = %d, want 4 (err = %v)", got, err)
			}
			assertMatch(t, got, "", "")

			got, err = d.FindBook(context.Background(), book)
			if err != nil {
				t.Fatalf("FindBook() after failure error = %v", err)
			}
			assertMatch(t, got, "exact", "page-exact")
			assertRequests(t, requests(), len(replies))
		})
	}
}
