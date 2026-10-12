package source

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

func date(year int, month time.Month, day int) *time.Time {
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &t
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func formatBooks(books []domain.Book) string {
	var b strings.Builder
	for _, book := range books {
		at := "<nil>"
		if book.LastAnnotatedAt != nil {
			at = book.LastAnnotatedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "{ID:%q ASIN:%q Title:%q Author:%q LastAnnotatedAt:%s} ", book.ID, book.ASIN, book.Title, book.Author, at)
	}
	return b.String()
}

func TestParseLibraryPage(t *testing.T) {
	// noValueBookHTML は Book 1 冊の断片。続きの印の input には value 属性が無い。
	const noValueBookHTML = `<div id="AAAAAAAAA6" class="a-row kp-notebook-library-each-book">` +
		`<h2 class="kp-notebook-searchable">合成の書名 G</h2><p class="kp-notebook-searchable">合成 著者 G</p>` +
		`<input type="hidden" id="kp-notebook-annotated-date-AAAAAAAAA6" value="2026年1月6日火曜日"></div>` +
		`<input type="hidden" class="kp-notebook-library-next-page-start">`
	tests := []struct {
		name     string
		fixture  string
		html     string // fixture が空なら使う
		fragment bool   // true なら ParseLibraryFragment、false なら ParseLibraryPage で解析する
		want     LibraryPage
	}{
		{
			name:    "続きのある 1 ページ目",
			fixture: "library_first.html",
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA1", ASIN: "AAAAAAAAA1", Title: "合成の書名 A", Author: "合成 著者 A", LastAnnotatedAt: date(2026, time.October, 8)},
					{ID: "AAAAAAAAA2", ASIN: "AAAAAAAAA2", Title: "合成の書名 B", Author: "合成 著者 B", LastAnnotatedAt: date(2025, time.December, 31)},
				},
				NextToken: "aaaa9999AAAA%2Baaaa9999%3D",
			},
		},
		{
			name:    "root はあるが Book が 0 冊で続きの無い 1 ページ目",
			fixture: "library_first_empty.html",
			want:    LibraryPage{},
		},
		{
			name:     "続きのある 2 ページ目以降の断片",
			fixture:  "library_next.html",
			fragment: true,
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA3", ASIN: "AAAAAAAAA3", Title: "合成の書名 C", Author: "合成 著者 C", LastAnnotatedAt: date(2026, time.March, 1)},
					{ID: "AAAAAAAAA4", ASIN: "AAAAAAAAA4", Title: "合成の書名 D", Author: "合成 著者 D"},
				},
				NextToken: "aaaa9999BBBB%2Baaaa9999%3D",
			},
		},
		{
			name:     "Book が 0 件でも続きのある断片",
			fixture:  "library_empty.html",
			fragment: true,
			want:     LibraryPage{NextToken: "aaaa9999CCCC%2Baaaa9999%3D"},
		},
		{
			name:     "最後のページ",
			fixture:  "library_last.html",
			fragment: true,
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA5", ASIN: "AAAAAAAAA5", Title: "合成の書名 F", Author: "合成 著者 F", LastAnnotatedAt: date(2026, time.January, 5)},
				},
			},
		},
		{
			name:     "value 属性の無い続きの印で終わる Book 0 件の断片",
			fixture:  "library_last_no_value.html",
			fragment: true,
			want:     LibraryPage{},
		},
		{
			name: "続きの印に value 属性の無い 1 ページ目",
			html: `<div id="kp-notebook-library" class="a-row">` + noValueBookHTML + `</div>`,
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA6", ASIN: "AAAAAAAAA6", Title: "合成の書名 G", Author: "合成 著者 G", LastAnnotatedAt: date(2026, time.January, 6)},
				},
			},
		},
		{
			name:     "続きの印に value 属性の無い Book ありの断片",
			html:     noValueBookHTML,
			fragment: true,
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA6", ASIN: "AAAAAAAAA6", Title: "合成の書名 G", Author: "合成 著者 G", LastAnnotatedAt: date(2026, time.January, 6)},
				},
			},
		},
		{
			name:     "ASIN の無い Book",
			fixture:  "library_no_asin.html",
			fragment: true,
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "b1-b2a5dc299814d91a43fac695591953db", Title: "合成の書名 ASIN なし", Author: "合成 著者 E", LastAnnotatedAt: date(2024, time.February, 29)},
				},
			},
		},
		{
			name:     "著者の要素に「著者: 」の接頭辞がある Book",
			fixture:  "library_author_prefix.html",
			fragment: true,
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA7", ASIN: "AAAAAAAAA7", Title: "合成の書名 H", Author: "合成 著者 H"},
					{ID: "AAAAAAAAA8", ASIN: "AAAAAAAAA8", Title: "合成の書名 I", Author: "合成 著者 I"},
					{ID: "AAAAAAAAA9", ASIN: "AAAAAAAAA9", Title: "合成の書名 J", Author: "著者 合成 J"},
					{ID: "AAAAAAAAB1", ASIN: "AAAAAAAAB1", Title: "合成の書名 K", Author: ""},
					{ID: "b1-b2a5dc299814d91a43fac695591953db", Title: "合成の書名 ASIN なし", Author: "合成 著者 E"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(tt.html)
			if tt.fixture != "" {
				input = readFixture(t, tt.fixture)
			}
			name, parse := libraryParser(tt.fragment)
			got, err := parse(input)
			if err != nil {
				t.Fatalf("%s(%s) error = %v", name, tt.fixture, err)
			}
			if !reflect.DeepEqual(got.Books, tt.want.Books) {
				t.Errorf("%s(%s).Books = %s, want %s", name, tt.fixture, formatBooks(got.Books), formatBooks(tt.want.Books))
			}
			if got.NextToken != tt.want.NextToken {
				t.Errorf("%s(%s).NextToken = %q, want %q", name, tt.fixture, got.NextToken, tt.want.NextToken)
			}
		})
	}
}

// libraryParser は fragment に応じて ParseLibraryFragment / ParseLibraryPage とその名前を返す。
func libraryParser(fragment bool) (string, func([]byte) (LibraryPage, error)) {
	if fragment {
		return "ParseLibraryFragment", ParseLibraryFragment
	}
	return "ParseLibraryPage", ParseLibraryPage
}

func TestParseLibraryPageError(t *testing.T) {
	const (
		title  = "合成の書名 エラー"
		author = "合成 著者 エラー"
	)
	// bookHTML は root の中に Book 2 冊を置き、2 冊目の Last Annotated Date を dateValue にした 1 ページ目の HTML。
	bookHTML := func(dateValue string) string {
		return `<div id="kp-notebook-library" class="a-row">` +
			`<div id="AAAAAAAAA1" class="a-row kp-notebook-library-each-book">` +
			`<h2 class="kp-notebook-searchable">合成の書名 A</h2><p class="kp-notebook-searchable">合成 著者 A</p>` +
			`<input type="hidden" id="kp-notebook-annotated-date-AAAAAAAAA1" value="2026年10月8日木曜日"></div>` +
			`<div id="AAAAAAAAA2" class="a-row kp-notebook-library-each-book">` +
			`<h2 class="kp-notebook-searchable">` + title + `</h2><p class="kp-notebook-searchable">` + author + `</p>` +
			`<input type="hidden" id="kp-notebook-annotated-date-AAAAAAAAA2" value="` + dateValue + `"></div></div>` +
			`<input type="hidden" class="kp-notebook-library-next-page-start" value="">`
	}
	// oneBookHTML は root の無い Book 1 冊の断片。続きの印は tail で足す。
	oneBookHTML := func(tail string) string {
		return `<div id="AAAAAAAAA1" class="a-row kp-notebook-library-each-book">` +
			`<h2 class="kp-notebook-searchable">` + title + `</h2><p class="kp-notebook-searchable">` + author + `</p></div>` + tail
	}
	const (
		wantNoRoot      = "source: library page: notebook root container is missing: unexpected notebook structure"
		wantNoNextInput = "source: library page: next page token input is missing: unexpected notebook structure"
	)
	tests := []struct {
		name       string
		fixture    string // 空なら html を使う
		html       string
		fragment   bool // true なら ParseLibraryFragment、false なら ParseLibraryPage で解析する
		want       string
		wantFormat bool // true なら exit 7 の SourceFormat（ErrSourceFormat）、false なら種類なし（exit 1）
		notWant    []string
	}{
		{
			name:    "月と日がゼロ埋め",
			html:    bookHTML("2026年01月05日月曜日"),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author, "2026年01月05日月曜日"},
		},
		{
			name:    "曜日が無い",
			html:    bookHTML("2026年10月8日"),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author, "2026年10月8日"},
		},
		{
			name:    ".com 系の英語表記",
			html:    bookHTML("Thursday October 8, 2026"),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author, "Thursday"},
		},
		{
			name:    "空の value",
			html:    bookHTML(""),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author},
		},
		{
			name:    "存在しない日付",
			html:    bookHTML("2026年2月30日月曜日"),
			want:    "source: library page: book 2: last annotated date is not a valid calendar date",
			notWant: []string{title, author, "2026年2月30日月曜日"},
		},
		{
			name:    "日付と合わない曜日",
			html:    bookHTML("2026年10月8日金曜日"),
			want:    "source: library page: book 2: last annotated date has a weekday that does not match the date",
			notWant: []string{title, author, "2026年10月8日金曜日"},
		},
		{
			name:       "1 ページ目に root（Book 一覧の container）が無い",
			fixture:    "library_no_root.html",
			want:       wantNoRoot,
			wantFormat: true,
			notWant:    []string{"合成の書名 root なし", "合成 著者 root なし", "AAAAAAAAA1", "<div", "kp-notebook"},
		},
		{
			name:       "1 ページ目に root も続きの印も無い（root の検査が先）",
			html:       oneBookHTML(""),
			want:       wantNoRoot,
			wantFormat: true,
			notWant:    []string{title, author},
		},
		{
			name:       "1 ページ目に続きの token も最後のページの印も無い",
			fixture:    "library_no_end_marker.html",
			want:       wantNoNextInput,
			wantFormat: true,
			notWant:    []string{"合成の書名 印なし", "合成 著者 印なし", "AAAAAAAAA1", "<div", "kp-notebook"},
		},
		{
			name:       "断片に続きの token も最後のページの印も無い",
			html:       oneBookHTML(""),
			fragment:   true,
			want:       wantNoNextInput,
			wantFormat: true,
			notWant:    []string{title, author},
		},
		{
			name:    "HTML として解析できない",
			html:    strings.Repeat("<div>", 600) + title + author,
			want:    "source: library page: parse HTML: html: open stack of elements exceeds 512 nodes",
			notWant: []string{title, author},
		},
		{
			name:     "断片が HTML として解析できない",
			html:     strings.Repeat("<div>", 600) + title + author,
			fragment: true,
			want:     "source: library page: parse HTML: html: open stack of elements exceeds 512 nodes",
			notWant:  []string{title, author},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := []byte(tt.html)
			if tt.fixture != "" {
				html = readFixture(t, tt.fixture)
			}
			name, parse := libraryParser(tt.fragment)
			_, err := parse(html)
			if err == nil {
				t.Fatalf("%s() error = nil, want error", name)
			}
			msg := err.Error()
			if msg != tt.want {
				t.Errorf("%s() error = %q, want %q", name, msg, tt.want)
			}
			for _, s := range tt.notWant {
				if strings.Contains(msg, s) {
					t.Errorf("%s() error = %q, want it not to contain %q", name, msg, s)
				}
			}
			wantCode := 1
			if tt.wantFormat {
				wantCode = 7
			}
			if got := exitcode.Code(err); got != wantCode {
				t.Errorf("exitcode.Code(err) = %d, want %d", got, wantCode)
			}
			if errors.Is(err, ErrSourceFormat) != tt.wantFormat {
				t.Errorf("errors.Is(err, ErrSourceFormat) = %v, want %v", !tt.wantFormat, tt.wantFormat)
			}
		})
	}
}
