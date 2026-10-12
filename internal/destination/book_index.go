package destination

import (
	"slices"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

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

// BookIndex は §10 のメモリ上の index。値は同じ key の候補をすべて持ち、Kindle ID Property が空のページは adoptable にも保持する。
// ASIN / Title+Author / Title の index には Kindle ID Property が空のページ（Adopt の候補、§11）だけを入れる。
// 空の値（空の Kindle ID Property・ASIN、正規化して空になる Title・Author）は key にしないので、空どうしは一致しない。
// byID はすべてのページをページ ID で持ち、AddCreated / Adopt / Update で書いた値を反映した今の値を返す。
type BookIndex struct {
	byID                    map[string]IndexedBook
	byKindleID              map[string][]IndexedBook
	byASIN                  map[string][]IndexedBook
	byNormalizedTitleAuthor map[titleAuthorKey][]IndexedBook
	byNormalizedTitle       map[string][]IndexedBook
	adoptable               map[string]IndexedBook
	hasAuthorMapping        bool
}

// NewBookIndex は books から index を作る。hasAuthorMapping が false なら Author を照合に使わない。
func NewBookIndex(books []IndexedBook, hasAuthorMapping bool) *BookIndex {
	ix := &BookIndex{
		byID:                    make(map[string]IndexedBook),
		byKindleID:              make(map[string][]IndexedBook),
		byASIN:                  make(map[string][]IndexedBook),
		byNormalizedTitleAuthor: make(map[titleAuthorKey][]IndexedBook),
		byNormalizedTitle:       make(map[string][]IndexedBook),
		adoptable:               make(map[string]IndexedBook),
		hasAuthorMapping:        hasAuthorMapping,
	}
	for _, b := range books {
		ix.add(b)
	}
	return ix
}

func (ix *BookIndex) add(b IndexedBook) {
	ix.byID[b.Remote.ID] = b
	if b.KindleID != "" {
		ix.byKindleID[b.KindleID] = append(ix.byKindleID[b.KindleID], b)
		return
	}
	ix.adoptable[b.Remote.ID] = b
	if b.ASIN != "" {
		ix.byASIN[b.ASIN] = append(ix.byASIN[b.ASIN], b)
	}
	title := domain.NormalizeForMatch(b.Title)
	if title == "" {
		return
	}
	ix.byNormalizedTitle[title] = append(ix.byNormalizedTitle[title], b)
	if author := ix.normalizedAuthor(b.Author); author != "" {
		key := titleAuthorKey{title: title, author: author}
		ix.byNormalizedTitleAuthor[key] = append(ix.byNormalizedTitleAuthor[key], b)
	}
}

func (ix *BookIndex) remove(b IndexedBook) {
	delete(ix.byID, b.Remote.ID)
	if b.KindleID != "" {
		removeCandidate(ix.byKindleID, b.KindleID, b.Remote.ID)
		return
	}
	if b.ASIN != "" {
		removeCandidate(ix.byASIN, b.ASIN, b.Remote.ID)
	}
	title := domain.NormalizeForMatch(b.Title)
	if title != "" {
		removeCandidate(ix.byNormalizedTitle, title, b.Remote.ID)
		if author := ix.normalizedAuthor(b.Author); author != "" {
			removeCandidate(ix.byNormalizedTitleAuthor, titleAuthorKey{title: title, author: author}, b.Remote.ID)
		}
	}
	delete(ix.adoptable, b.Remote.ID)
}

func removeCandidate[K comparable](index map[K][]IndexedBook, key K, id string) {
	candidates := slices.DeleteFunc(index[key], func(b IndexedBook) bool {
		return b.Remote.ID == id
	})
	if len(candidates) == 0 {
		delete(index, key)
		return
	}
	index[key] = candidates
}

// AddCreated は作成したページを、Kindle ID Property に bookID が入ったページとして index に足す。
// Match が none の Book にだけ使う。同じ Book ID を 2 回足すとその Book ID の照合は ambiguous になる。
func (ix *BookIndex) AddCreated(remote RemoteBook, bookID string) {
	ix.add(IndexedBook{Remote: remote, KindleID: bookID})
}

// Adopt は Adopt 候補のページを Kindle ID Property に bookID が入ったものとして index に反映する。
// そのページは ASIN / Title+Author / Title の候補から外れ、以後 bookID の Book とだけ exact で一致する。
func (ix *BookIndex) Adopt(remote RemoteBook, bookID string) {
	b, ok := ix.adoptable[remote.ID]
	if ok {
		ix.remove(b)
	}
	b.Remote = remote
	b.KindleID = bookID
	ix.add(b)
}

// Update は remote のページに書いた author / asin を index に反映する（§12 の Adopt / Subsequent Sync 列）。
// values で nil の Property は変えない。last_annotated_at は照合にも次の判定にも使わないので持たない。
// index に無いページなら何もしない。
func (ix *BookIndex) Update(remote RemoteBook, values BookPropertyValues) {
	b, ok := ix.byID[remote.ID]
	if !ok {
		return
	}
	ix.remove(b)
	if values.Author != nil {
		b.Author = values.Author.Value
	}
	if values.ASIN != nil {
		b.ASIN = values.ASIN.Value
	}
	ix.add(b)
}

// Lookup は remote のページの今の値を返す。index に無いページなら ok が false。
// AddCreated / Adopt / Update で反映した値を含む。
func (ix *BookIndex) Lookup(remote RemoteBook) (IndexedBook, bool) {
	b, ok := ix.byID[remote.ID]
	return b, ok
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
