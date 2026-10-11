package destination

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

const (
	testExactPageID     = "page-exact"
	testUpdatePagePath  = "/v1/pages/page-exact"
	updatedPageResponse = `{"object":"page","id":"page-exact"}`
)

func TestNotionDestinationUpdateBook(t *testing.T) {
	// exact のページ（Kindle ID Property が Book ID）の author / asin が埋まっているかで body が変わる。
	// title は Kindle とページで違えても書かず、Kindle ID Property も書かない
	fullBook := kindleBook(testOtherASIN, "Kindle Title", "Alan")
	fullBook.LastAnnotatedAt = &annotatedAt
	tests := []struct {
		name           string
		mapping        config.Mapping
		page           string // 更新するページ（ID は page-exact）
		book           domain.Book
		wantProperties map[string]any // nil なら更新の要求を送らない
		wantIndexed    IndexedBook    // 更新のあと index が返す今の値
	}{
		{
			name:    "author / asin が空のページには Kindle の値を書き、last_annotated_at を日付だけの date で書き、title と Kindle ID Property は書かない",
			mapping: writeMapping,
			page:    bookPage(testExactPageID, testOtherASIN, "", "Notion Title", ""),
			book:    fullBook,
			wantProperties: map[string]any{
				authorPropertyID:          wantRichText("Alan"),
				asinPropertyID:            wantRichText(testOtherASIN),
				lastAnnotatedAtPropertyID: wantDate("2026-10-01"),
			},
			wantIndexed: page(testExactPageID, testOtherASIN, testOtherASIN, "Notion Title", "Alan"),
		},
		{
			name:    "author / asin が入っているページには Kindle の値を書かず、last_annotated_at だけ書く",
			mapping: writeMapping,
			page:    bookPage(testExactPageID, testOtherASIN, testOtherASIN, "Notion Title", "Notion Author"),
			book:    fullBook,
			wantProperties: map[string]any{
				lastAnnotatedAtPropertyID: wantDate("2026-10-01"),
			},
			wantIndexed: page(testExactPageID, testOtherASIN, testOtherASIN, "Notion Title", "Notion Author"),
		},
		{
			name:    "author / asin が空白だけのページは空として Kindle の値を書く",
			mapping: writeMapping,
			page:    bookPage(testExactPageID, testOtherASIN, " ", "Notion Title", "\u3000"),
			book:    fullBook,
			wantProperties: map[string]any{
				authorPropertyID:          wantRichText("Alan"),
				asinPropertyID:            wantRichText(testOtherASIN),
				lastAnnotatedAtPropertyID: wantDate("2026-10-01"),
			},
			wantIndexed: page(testExactPageID, testOtherASIN, testOtherASIN, "Notion Title", "Alan"),
		},
		{
			name:    "mapping の無い author / asin / last_annotated_at は空でも書かない",
			mapping: config.Mapping{Title: titlePropertyID, KindleID: kindleIDPropertyID, Author: authorPropertyID},
			page:    bookPage(testExactPageID, testOtherASIN, "", "Notion Title", ""),
			book:    fullBook,
			wantProperties: map[string]any{
				authorPropertyID: wantRichText("Alan"),
			},
			wantIndexed: page(testExactPageID, testOtherASIN, "", "Notion Title", "Alan"),
		},
		{
			name:        "author / asin が入っていて Kindle の LastAnnotatedAt が無ければ、要求を送らずに成功する",
			mapping:     writeMapping,
			page:        bookPage(testExactPageID, testOtherASIN, testOtherASIN, "Notion Title", "Notion Author"),
			book:        kindleBook(testOtherASIN, "Kindle Title", "Alan"),
			wantIndexed: page(testExactPageID, testOtherASIN, testOtherASIN, "Notion Title", "Notion Author"),
		},
		{
			name:        "author / asin が空でも mapping が title と Kindle ID だけなら、要求を送らずに成功する",
			mapping:     config.Mapping{Title: titlePropertyID, KindleID: kindleIDPropertyID},
			page:        bookPage(testExactPageID, testOtherASIN, "", "Notion Title", ""),
			book:        fullBook,
			wantIndexed: page(testExactPageID, testOtherASIN, "", "Notion Title", ""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replies := []notionReply{queryReply("", tt.page)}
			if tt.wantProperties != nil {
				replies = append(replies, notionReply{body: updatedPageResponse})
			}
			d, requests := newWriteDestination(t, tt.mapping, replies...)
			remote := RemoteBook{ID: testExactPageID}

			// FindBook より先に呼んでも index を作ってから今の値を引く
			if err := d.UpdateBook(context.Background(), remote, tt.book); err != nil {
				t.Fatalf("UpdateBook() error = %v", err)
			}
			reqs := requests()
			if tt.wantProperties == nil {
				assertRequestLines(t, reqs, "POST "+testQueryPath)
			} else {
				// 更新 1 冊につき更新の要求は 1 回だけ
				assertRequestLines(t, reqs, "POST "+testQueryPath, "PATCH "+testUpdatePagePath)
				if properties := decodeProperties(t, reqs[1].body, ""); !reflect.DeepEqual(properties, tt.wantProperties) {
					t.Errorf("body.properties = %v, want %v", properties, tt.wantProperties)
				}
			}
			if got, ok := d.index.Lookup(remote); !ok || got != tt.wantIndexed {
				t.Errorf("index.Lookup() = %+v, %t, want %+v, true", got, ok, tt.wantIndexed)
			}
		})
	}
}

func TestNotionDestinationUpdateBookAfterFindBook(t *testing.T) {
	book := kindleBook(testOtherASIN, "Kindle Title", "Alan")
	book.LastAnnotatedAt = &annotatedAt
	d, requests := newWriteDestination(t, writeMapping,
		queryReply("", bookPage(testExactPageID, testOtherASIN, "", "Notion Title", "")),
		notionReply{body: updatedPageResponse})

	got, err := d.FindBook(context.Background(), book)
	if err != nil {
		t.Fatalf("FindBook() error = %v", err)
	}
	assertMatch(t, got, "exact", testExactPageID)
	if err := d.UpdateBook(context.Background(), *got.Book, book); err != nil {
		t.Fatalf("UpdateBook() error = %v", err)
	}
	// FindBook で作った index を使い、Data Source は取り直さない
	assertRequestLines(t, requests(), "POST "+testQueryPath, "PATCH "+testUpdatePagePath)
}

func TestNotionDestinationUpdateBookNotIndexed(t *testing.T) {
	book := kindleBook(testOtherASIN, "Kindle Title", "Alan")
	book.LastAnnotatedAt = &annotatedAt
	d, requests := newWriteDestination(t, writeMapping,
		queryReply("", bookPage(testExactPageID, testOtherASIN, "", "Notion Title", "")))

	err := d.UpdateBook(context.Background(), RemoteBook{ID: "page-unknown"}, book)
	if err == nil {
		t.Fatal("UpdateBook() error = nil, want error")
	}
	// index を作る全件取得はするが、更新の要求は送らない
	assertRequestLines(t, requests(), "POST "+testQueryPath)
}

func TestNotionDestinationUpdateBookFailure(t *testing.T) {
	book := kindleBook(testOtherASIN, "Kindle Title", "Alan")
	book.LastAnnotatedAt = &annotatedAt
	exactPage := bookPage(testExactPageID, testOtherASIN, "", "Notion Title", "")
	failure := notionReply{status: http.StatusBadRequest, body: notionErrorBody}
	tests := []struct {
		name         string
		replies      []notionReply
		wantRequests []string
	}{
		{
			name:         "index の全件取得に失敗したらページを更新せず、次の FindBook で取り直す",
			replies:      []notionReply{failure, queryReply("", exactPage)},
			wantRequests: []string{"POST " + testQueryPath, "POST " + testQueryPath},
		},
		{
			name:         "ページの更新に失敗したら index を変えない",
			replies:      []notionReply{queryReply("", exactPage), failure},
			wantRequests: []string{"POST " + testQueryPath, "PATCH " + testUpdatePagePath},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, requests := newWriteDestination(t, writeMapping, tt.replies...)
			remote := RemoteBook{ID: testExactPageID}

			err := d.UpdateBook(context.Background(), remote, book)
			if got := exitcode.Code(err); got != 4 {
				t.Fatalf("exitcode.Code(err) = %d, want 4 (err = %v)", got, err)
			}
			var status *notion.StatusError
			if !errors.As(err, &status) || status.Status != http.StatusBadRequest {
				t.Errorf("StatusError = %+v, want Status 400", status)
			}

			// 書けていないので、そのページは exact のまま author / asin も書込前の値
			got, err := d.FindBook(context.Background(), book)
			if err != nil {
				t.Fatalf("FindBook() after failure error = %v", err)
			}
			assertMatch(t, got, "exact", testExactPageID)
			want := page(testExactPageID, testOtherASIN, "", "Notion Title", "")
			if indexed, ok := d.index.Lookup(remote); !ok || indexed != want {
				t.Errorf("index.Lookup() = %+v, %t, want %+v, true", indexed, ok, want)
			}
			assertRequestLines(t, requests(), tt.wantRequests...)
		})
	}
}
