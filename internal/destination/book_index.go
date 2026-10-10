package destination

import "github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"

// IndexedBook は同期先の 1 ページを、照合に要る値だけで表したもの。
type IndexedBook struct {
	Remote   RemoteBook
	KindleID string // 空なら Adopt の候補
	ASIN     string
	Title    string
	Author   string
}

type titleAuthorKey struct {
	title, author string
}

// BookIndex は §10 のメモリ上の index。値は同じ key の候補をすべて持つ。
// ASIN / Title+Author / Title の index には Kindle ID Property が空のページ（Adopt の候補、§11）だけを入れる。
// 空の値（空の Kindle ID Property・ASIN、正規化して空になる Title・Author）は key にしないので、空どうしは一致しない。
type BookIndex struct {
	byKindleID              map[string][]IndexedBook
	byASIN                  map[string][]IndexedBook
	byNormalizedTitleAuthor map[titleAuthorKey][]IndexedBook
	byNormalizedTitle       map[string][]IndexedBook
	hasAuthorMapping        bool
}

// NewBookIndex は books から index を作る。hasAuthorMapping が false なら Author を照合に使わない。
func NewBookIndex(books []IndexedBook, hasAuthorMapping bool) *BookIndex {
	ix := &BookIndex{
		byKindleID:              make(map[string][]IndexedBook),
		byASIN:                  make(map[string][]IndexedBook),
		byNormalizedTitleAuthor: make(map[titleAuthorKey][]IndexedBook),
		byNormalizedTitle:       make(map[string][]IndexedBook),
		hasAuthorMapping:        hasAuthorMapping,
	}
	for _, b := range books {
		if b.KindleID != "" {
			ix.byKindleID[b.KindleID] = append(ix.byKindleID[b.KindleID], b)
			continue
		}
		if b.ASIN != "" {
			ix.byASIN[b.ASIN] = append(ix.byASIN[b.ASIN], b)
		}
		title := domain.NormalizeForMatch(b.Title)
		if title == "" {
			continue
		}
		ix.byNormalizedTitle[title] = append(ix.byNormalizedTitle[title], b)
		if author := ix.normalizedAuthor(b.Author); author != "" {
			key := titleAuthorKey{title: title, author: author}
			ix.byNormalizedTitleAuthor[key] = append(ix.byNormalizedTitleAuthor[key], b)
		}
	}
	return ix
}

// Match は §9 の順（Kindle ID → ASIN → Title+Author → Title だけ）で book を照合する。
// 候補が 2 件以上の段があれば、そこで ambiguous として止まる。
// Title+Author の段は author が両方ある組だけを比べ、author が空の側は Title だけの段で扱う。
func (ix *BookIndex) Match(book domain.Book) BookMatch {
	if m, ok := decide(ix.byKindleID[book.ID], BookMatchExact); ok {
		return m
	}
	if m, ok := decide(ix.byASIN[book.ASIN], BookMatchAdoptable); ok {
		return m
	}
	title := domain.NormalizeForMatch(book.Title)
	author := ix.normalizedAuthor(book.Author)
	if m, ok := decide(ix.byNormalizedTitleAuthor[titleAuthorKey{title: title, author: author}], BookMatchAdoptable); ok {
		return m
	}
	var candidates []IndexedBook
	for _, b := range ix.byNormalizedTitle[title] {
		if author == "" || ix.normalizedAuthor(b.Author) == "" {
			candidates = append(candidates, b)
		}
	}
	if m, ok := decide(candidates, BookMatchAdoptable); ok {
		return m
	}
	return BookMatch{Status: BookMatchNone}
}

// normalizedAuthor は照合用の author を返す。author の対応が無ければ常に空。
func (ix *BookIndex) normalizedAuthor(author string) string {
	if !ix.hasAuthorMapping {
		return ""
	}
	return domain.NormalizeForMatch(author)
}

// decide は 1 つの段の候補から結果を決める。候補が無ければ ok が false で、次の段に進む。
func decide(candidates []IndexedBook, status BookMatchStatus) (BookMatch, bool) {
	switch len(candidates) {
	case 0:
		return BookMatch{}, false
	case 1:
		remote := candidates[0].Remote
		return BookMatch{Status: status, Book: &remote}, true
	default:
		return BookMatch{Status: BookMatchAmbiguous}, true
	}
}
