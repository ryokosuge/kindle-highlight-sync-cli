package source

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

const (
	librarySelectorRoot      = "#kp-notebook-library"
	librarySelectorBook      = "div.kp-notebook-library-each-book"
	librarySelectorTitle     = "h2.kp-notebook-searchable"
	librarySelectorAuthor    = "p.kp-notebook-searchable"
	librarySelectorDate      = "input[id^='kp-notebook-annotated-date']"
	librarySelectorNextToken = "input.kp-notebook-library-next-page-start"
)

var (
	errAnnotatedDateForm     = errors.New("last annotated date is not in the YYYY年M月D日<曜日> form")
	errAnnotatedDateCalendar = errors.New("last annotated date is not a valid calendar date")
	errAnnotatedDateWeekday  = errors.New("last annotated date has a weekday that does not match the date")

	annotatedDatePattern = regexp.MustCompile(`^([0-9]{4})年([1-9]|1[0-2])月([1-9]|[12][0-9]|3[01])日([日月火水木金土])曜日$`)
	weekdayNames         = [...]string{"日", "月", "火", "水", "木", "金", "土"}
)

// LibraryPage は Notebook の Book 一覧 1 ページ分の解析結果。
type LibraryPage struct {
	Books     []domain.Book
	NextToken string // 空なら最後のページ（value 属性の無い input も空として読む）
}

// ParseLibraryPage は Notebook の Book 一覧 1 ページ目の HTML を解析する。
// root（#kp-notebook-library）と続きの印の input が無ければ SourceFormat の種類のエラーを返す。
// 続きの印の value が空、または value 属性が無ければ最後のページとして NextToken を空にする。
// 2 ページ目以降は root を持たない断片なので ParseLibraryFragment を使う（仕様書 §18.4）。
func ParseLibraryPage(html []byte) (LibraryPage, error) {
	return parseLibrary(html, true)
}

// ParseLibraryFragment は Notebook の Book 一覧 2 ページ目以降の断片 HTML を解析する。
// 続きの印の input が無ければ SourceFormat の種類のエラーを返す。value が空、または value 属性が無ければ最後のページ。
func ParseLibraryFragment(html []byte) (LibraryPage, error) {
	return parseLibrary(html, false)
}

func parseLibrary(html []byte, wantRoot bool) (LibraryPage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return LibraryPage{}, fmt.Errorf("source: library page: parse HTML: %w", err)
	}

	if wantRoot && doc.Find(librarySelectorRoot).Length() == 0 {
		return LibraryPage{}, fmt.Errorf("source: library page: %w", newFormatError("notebook root container is missing"))
	}

	next := doc.Find(librarySelectorNextToken).First()
	if next.Length() == 0 {
		return LibraryPage{}, fmt.Errorf("source: library page: %w", newFormatError("next page token input is missing"))
	}

	// value 属性の無い input も、value="" と同じく最後のページの印として読む（実機の Book 一覧の最後のページはこの形で返る）。
	page := LibraryPage{NextToken: next.AttrOr("value", "")}
	var parseErr error
	doc.Find(librarySelectorBook).EachWithBreak(func(i int, s *goquery.Selection) bool {
		book, err := parseLibraryBook(s)
		if err != nil {
			parseErr = fmt.Errorf("source: library page: book %d: %w", i+1, err)
			return false
		}
		page.Books = append(page.Books, book)
		return true
	})
	if parseErr != nil {
		return LibraryPage{}, parseErr
	}
	return page, nil
}

func parseLibraryBook(s *goquery.Selection) (domain.Book, error) {
	asin := strings.TrimSpace(s.AttrOr("id", ""))
	title := strings.TrimSpace(s.Find(librarySelectorTitle).First().Text())
	author := strings.TrimSpace(s.Find(librarySelectorAuthor).First().Text())
	book := domain.Book{
		ID:     domain.NewBookID(asin, title, author),
		ASIN:   asin,
		Title:  title,
		Author: author,
	}
	date := s.Find(librarySelectorDate).First()
	if date.Length() == 0 {
		return book, nil
	}
	t, err := parseAnnotatedDate(date.AttrOr("value", ""))
	if err != nil {
		return domain.Book{}, err
	}
	book.LastAnnotatedAt = &t
	return book, nil
}

// parseAnnotatedDate は §18.3 の YYYY年M月D日<曜日> を、その日の 0 時 (UTC) として解釈する。
func parseAnnotatedDate(v string) (time.Time, error) {
	m := annotatedDatePattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return time.Time{}, errAnnotatedDateForm
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Day() != day {
		return time.Time{}, errAnnotatedDateCalendar
	}
	if weekdayNames[t.Weekday()] != m[4] {
		return time.Time{}, errAnnotatedDateWeekday
	}
	return t, nil
}
