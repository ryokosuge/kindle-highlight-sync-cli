package destination

import (
	"reflect"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

const (
	testASIN      = "B000000001"
	testOtherASIN = "B000000002"
)

// kindleBook は Kindle 側の Book を NewBookID の Book ID 付きで作る。
func kindleBook(asin, title, author string) domain.Book {
	return domain.Book{ID: domain.NewBookID(asin, title, author), ASIN: asin, Title: title, Author: author}
}

func page(id, kindleID, asin, title, author string) IndexedBook {
	return IndexedBook{Remote: RemoteBook{ID: id}, KindleID: kindleID, ASIN: asin, Title: title, Author: author}
}

func TestBookIndexMatch(t *testing.T) {
	noASINBook := kindleBook("", "吾輩は猫である", "夏目漱石")
	otherBook := kindleBook(testOtherASIN, "Other", "Other")
	tests := []struct {
		name             string
		pages            []IndexedBook
		update           func(ix *BookIndex)
		hasAuthorMapping bool
		book             domain.Book
		wantStatus       BookMatchStatus
		wantID           string
	}{
		// Kindle ID Property の段
		{
			name: "Kindle ID Property が ASIN の Book ID と一致すれば ASIN や Title が一致する別のページより優先して exact",
			pages: []IndexedBook{
				page("page-asin", "", testASIN, "Go", "Alan"),
				page("page-title", "", "", "Go", "Alan"),
				page("page-kindle", testASIN, "", "Other", "Other"),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "exact",
			wantID:           "page-kindle",
		},
		{
			name: "ASIN の無い Book は b1- の Book ID で Kindle ID Property と一致して exact",
			pages: []IndexedBook{
				page("page-title", "", "", noASINBook.Title, noASINBook.Author),
				page("page-kindle", noASINBook.ID, "", "Other", ""),
			},
			hasAuthorMapping: true,
			book:             noASINBook,
			wantStatus:       "exact",
			wantID:           "page-kindle",
		},
		{
			name: "Kindle ID Property が一致するページが 2 件なら ambiguous で ASIN の段に進まない",
			pages: []IndexedBook{
				page("page-kindle-1", testASIN, "", "", ""),
				page("page-kindle-2", testASIN, "", "", ""),
				page("page-asin", "", testASIN, "Go", "Alan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "ambiguous",
		},
		{
			name: "作成したページを足すと、その Book ID の Book は ASIN が一致するページより優先して exact",
			pages: []IndexedBook{
				page("page-asin", "", testASIN, "Go", "Alan"),
			},
			update: func(ix *BookIndex) {
				ix.AddCreated(RemoteBook{ID: "page-created"}, kindleBook(testASIN, "Go", "Alan").ID)
			},
			book:       kindleBook(testASIN, "Go", "Alan"),
			wantStatus: "exact",
			wantID:     "page-created",
		},
		{
			name: "ASIN の無い Book も作成したページを足すと b1- の Book ID で exact",
			update: func(ix *BookIndex) {
				ix.AddCreated(RemoteBook{ID: "page-created"}, noASINBook.ID)
			},
			book:       noASINBook,
			wantStatus: "exact",
			wantID:     "page-created",
		},
		{
			name: "同じ Book ID で作成したページを 2 回足すと ambiguous",
			update: func(ix *BookIndex) {
				ix.AddCreated(RemoteBook{ID: "page-created"}, noASINBook.ID)
				ix.AddCreated(RemoteBook{ID: "page-created"}, noASINBook.ID)
			},
			book:       noASINBook,
			wantStatus: "ambiguous",
		},
		{
			name: "Adopt したページは、その Book ID の Book と Kindle ID Property の一致で exact",
			pages: []IndexedBook{
				page("page-title", "", "", noASINBook.Title, noASINBook.Author),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-title"}, noASINBook.ID)
			},
			book:       noASINBook,
			wantStatus: "exact",
			wantID:     "page-title",
		},
		// ASIN の段
		{
			name: "Kindle ID Property が空で ASIN が一致すれば Title+Author が一致する別のページより優先して adoptable",
			pages: []IndexedBook{
				page("page-title", "", "", "Go", "Alan"),
				page("page-asin", "", testASIN, "Other", "Other"),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "adoptable",
			wantID:           "page-asin",
		},
		{
			name: "ASIN が一致するページが 2 件なら ambiguous で Title+Author の段に進まない",
			pages: []IndexedBook{
				page("page-asin-1", "", testASIN, "", ""),
				page("page-asin-2", "", testASIN, "", ""),
				page("page-title", "", "", "Go", "Alan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "ambiguous",
		},
		{
			name: "ASIN は正規化せずに比べる",
			pages: []IndexedBook{
				page("page-asin", "", "b000000001", "", ""),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "none",
		},
		// Title+Author の段
		{
			name: "ASIN で決まらなければ正規化した Title+Author が一致するページが adoptable",
			pages: []IndexedBook{
				page("page-asin", "", testOtherASIN, "Other", "Other"),
				page("page-title-author", "", "", "ｇｏ　プログラミング", " ALAN\tDONOVAN "),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go プログラミング", "Alan Donovan"),
			wantStatus:       "adoptable",
			wantID:           "page-title-author",
		},
		{
			name: "Title+Author が一致すれば author が空で Title だけ一致するページより優先する",
			pages: []IndexedBook{
				page("page-title", "", "", "Go", ""),
				page("page-title-author", "", "", "Go", "Alan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "adoptable",
			wantID:           "page-title-author",
		},
		{
			name: "Title+Author が一致するページが 2 件なら ambiguous",
			pages: []IndexedBook{
				page("page-title-author-1", "", "", "Go", "Alan"),
				page("page-title-author-2", "", "", "go", "alan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "ambiguous",
		},
		{
			name: "Title+Author の key は境界がずれた組と衝突しない",
			pages: []IndexedBook{
				page("page-shifted", "", "", "GoA", "lan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		// Title だけの段
		{
			name: "author の対応が無ければ Title だけが一致する 1 件が adoptable",
			pages: []IndexedBook{
				page("page-title", "", "", "Go", "Bob"),
			},
			hasAuthorMapping: false,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "adoptable",
			wantID:           "page-title",
		},
		{
			name: "author の対応が無ければ author の違う同じ Title のページ 2 件は ambiguous",
			pages: []IndexedBook{
				page("page-title-1", "", "", "Go", "Alan"),
				page("page-title-2", "", "", "Go", "Bob"),
			},
			hasAuthorMapping: false,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "ambiguous",
		},
		{
			name: "Notion の author が空なら Title だけが一致する 1 件が adoptable",
			pages: []IndexedBook{
				page("page-title", "", "", "Go", " "),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "adoptable",
			wantID:           "page-title",
		},
		{
			name: "Kindle の author が空なら Title だけが一致する 1 件が adoptable",
			pages: []IndexedBook{
				page("page-title", "", "", "Go", "Bob"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", ""),
			wantStatus:       "adoptable",
			wantID:           "page-title",
		},
		{
			name: "author が両方あって違うページは候補に数えず、author が空の 1 件が adoptable",
			pages: []IndexedBook{
				page("page-other-author", "", "", "Go", "Bob"),
				page("page-no-author", "", "", "Go", ""),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "adoptable",
			wantID:           "page-no-author",
		},
		{
			name: "author が両方あって違うページだけなら none",
			pages: []IndexedBook{
				page("page-other-author", "", "", "Go", "Bob"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "Notion の author が空で Title だけが一致するページが 2 件なら ambiguous",
			pages: []IndexedBook{
				page("page-no-author-1", "", "", "Go", ""),
				page("page-no-author-2", "", "", "Go", ""),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "ambiguous",
		},
		{
			name: "Kindle の author が空なら author が空のページと author があるページの 2 件は ambiguous",
			pages: []IndexedBook{
				page("page-no-author", "", "", "Go", ""),
				page("page-author", "", "", "Go", "Bob"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", ""),
			wantStatus:       "ambiguous",
		},
		// Kindle ID Property が入っているページの除外
		{
			name: "Adopt したページは ASIN が一致する別の Book の候補にならない",
			pages: []IndexedBook{
				page("page-adopted", "", testASIN, "", ""),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-adopted"}, otherBook.ID)
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "Adopt したページは Title+Author が一致する別の Book の候補にならない",
			pages: []IndexedBook{
				page("page-adopted", "", "", "Go", "Alan"),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-adopted"}, otherBook.ID)
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "Adopt したページは Title だけが一致する別の Book の候補にならない",
			pages: []IndexedBook{
				page("page-adopted", "", "", "Go", ""),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-adopted"}, otherBook.ID)
			},
			hasAuthorMapping: false,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "ASIN が一致する 2 件のうち 1 件を Adopt すると残りの 1 件が adoptable",
			pages: []IndexedBook{
				page("page-asin-1", "", testASIN, "", ""),
				page("page-asin-2", "", testASIN, "", ""),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-asin-1"}, otherBook.ID)
			},
			book:       kindleBook(testASIN, "Go", "Alan"),
			wantStatus: "adoptable",
			wantID:     "page-asin-2",
		},
		{
			name: "Title+Author が一致する 2 件のうち 1 件を Adopt すると残りの 1 件が adoptable",
			pages: []IndexedBook{
				page("page-title-author-1", "", "", "Go", "Alan"),
				page("page-title-author-2", "", "", "Go", "Alan"),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-title-author-1"}, otherBook.ID)
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "adoptable",
			wantID:           "page-title-author-2",
		},
		{
			name: "Adopt したページは別の Book の Book ID とは一致しない",
			pages: []IndexedBook{
				page("page-adopted", "", testASIN, "Go", "Alan"),
			},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-adopted"}, otherBook.ID)
			},
			book:       kindleBook(testASIN, "Go", "Alan"),
			wantStatus: "none",
		},
		{
			name: "Kindle ID Property が入っているページは ASIN が一致しても候補にならない",
			pages: []IndexedBook{
				page("page-other-book", testOtherASIN, testASIN, "", ""),
			},
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "Kindle ID Property が入っているページは Title+Author が一致しても候補にならない",
			pages: []IndexedBook{
				page("page-other-book", testOtherASIN, "", "Go", "Alan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "Kindle ID Property が入っているページは Title だけが一致しても候補にならない",
			pages: []IndexedBook{
				page("page-other-book", testOtherASIN, "", "Go", ""),
			},
			hasAuthorMapping: false,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		// 空の値
		{
			name: "空の ASIN どうしは一致しない",
			pages: []IndexedBook{
				page("page-empty", "", "", "Other", "Other"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", "Go", "Alan"),
			wantStatus:       "none",
		},
		{
			name: "正規化して空になる Title どうしは一致しない",
			pages: []IndexedBook{
				page("page-blank-title", "", "", "\u3000", ""),
			},
			hasAuthorMapping: false,
			book:             kindleBook("", " ", ""),
			wantStatus:       "none",
		},
		{
			name: "正規化して空になる Title どうしは author が一致しても一致しない",
			pages: []IndexedBook{
				page("page-blank-title", "", "", "", "Alan"),
			},
			hasAuthorMapping: true,
			book:             kindleBook("", " ", "Alan"),
			wantStatus:       "none",
		},
		{
			name:             "ページが無ければ none",
			hasAuthorMapping: true,
			book:             kindleBook(testASIN, "Go", "Alan"),
			wantStatus:       "none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := NewBookIndex(tt.pages, tt.hasAuthorMapping)
			if tt.update != nil {
				tt.update(ix)
			}
			got := ix.Match(tt.book)
			if got.Status != tt.wantStatus {
				t.Errorf("Match().Status = %q, want %q", got.Status, tt.wantStatus)
			}
			var wantBook *RemoteBook
			if tt.wantID != "" {
				wantBook = &RemoteBook{ID: tt.wantID}
			}
			if !reflect.DeepEqual(got.Book, wantBook) {
				t.Errorf("Match().Book = %+v, want %+v", got.Book, wantBook)
			}
		})
	}
}

func TestBookIndexLookup(t *testing.T) {
	book := kindleBook(testASIN, "Go", "Alan")
	tests := []struct {
		name   string
		pages  []IndexedBook
		update func(ix *BookIndex)
		remote RemoteBook
		want   IndexedBook
		wantOK bool
	}{
		{
			name:   "Adopt の候補のページを ID で引ける",
			pages:  []IndexedBook{page("page-asin", "", testASIN, "Go", "Alan")},
			remote: RemoteBook{ID: "page-asin"},
			want:   page("page-asin", "", testASIN, "Go", "Alan"),
			wantOK: true,
		},
		{
			name:   "Kindle ID Property が入っているページも ID で引ける",
			pages:  []IndexedBook{page("page-kindle", testASIN, "", "Other", "Other")},
			remote: RemoteBook{ID: "page-kindle"},
			want:   page("page-kindle", testASIN, "", "Other", "Other"),
			wantOK: true,
		},
		{
			name:   "index に無いページは ok が false",
			pages:  []IndexedBook{page("page-asin", "", testASIN, "Go", "Alan")},
			remote: RemoteBook{ID: "page-missing"},
			wantOK: false,
		},
		{
			name:  "作成したページは Kindle ID Property に Book ID が入った値で引ける",
			pages: []IndexedBook{page("page-asin", "", testASIN, "Go", "Alan")},
			update: func(ix *BookIndex) {
				ix.AddCreated(RemoteBook{ID: "page-created"}, book.ID)
			},
			remote: RemoteBook{ID: "page-created"},
			want:   page("page-created", book.ID, "", "", ""),
			wantOK: true,
		},
		{
			name:  "Adopt したページは Kindle ID Property に Book ID が入り、他の値はそのまま",
			pages: []IndexedBook{page("page-asin", "", testASIN, "Go", "Alan")},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-asin"}, book.ID)
			},
			remote: RemoteBook{ID: "page-asin"},
			want:   page("page-asin", book.ID, testASIN, "Go", "Alan"),
			wantOK: true,
		},
		{
			name:  "Update で書いた author / asin が入り、nil の Property は変わらない",
			pages: []IndexedBook{page("page-title", "", "", "Go", " ")},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-title"}, book.ID)
				ix.Update(RemoteBook{ID: "page-title"}, BookPropertyValues{
					Author: &TextPropertyValue{PropertyID: authorPropertyID, Value: "Alan"},
					ASIN:   &TextPropertyValue{PropertyID: asinPropertyID, Value: testASIN},
				})
			},
			remote: RemoteBook{ID: "page-title"},
			want:   page("page-title", book.ID, testASIN, "Go", "Alan"),
			wantOK: true,
		},
		{
			name:  "Update で ASIN だけ書くと author は変わらない",
			pages: []IndexedBook{page("page-title", "", "", "Go", "Alan")},
			update: func(ix *BookIndex) {
				ix.Adopt(RemoteBook{ID: "page-title"}, book.ID)
				ix.Update(RemoteBook{ID: "page-title"}, BookPropertyValues{
					ASIN: &TextPropertyValue{PropertyID: asinPropertyID, Value: testASIN},
				})
			},
			remote: RemoteBook{ID: "page-title"},
			want:   page("page-title", book.ID, testASIN, "Go", "Alan"),
			wantOK: true,
		},
		{
			name:  "index に無いページへの Update は何もしない",
			pages: []IndexedBook{page("page-asin", "", testASIN, "Go", "Alan")},
			update: func(ix *BookIndex) {
				ix.Update(RemoteBook{ID: "page-missing"}, BookPropertyValues{
					Author: &TextPropertyValue{PropertyID: authorPropertyID, Value: "Alan"},
				})
			},
			remote: RemoteBook{ID: "page-missing"},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := NewBookIndex(tt.pages, true)
			if tt.update != nil {
				tt.update(ix)
			}
			got, ok := ix.Lookup(tt.remote)
			if ok != tt.wantOK {
				t.Errorf("Lookup() ok = %t, want %t", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("Lookup() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestBookIndexUpdateKeepsMatch は Update のあとも Kindle ID Property の照合が exact のままで、他の Book の候補に戻らないことを確かめる。
func TestBookIndexUpdateKeepsMatch(t *testing.T) {
	book := kindleBook(testASIN, "Go", "Alan")
	ix := NewBookIndex([]IndexedBook{page("page-title", "", "", "Go", "")}, true)
	ix.Adopt(RemoteBook{ID: "page-title"}, book.ID)
	ix.Update(RemoteBook{ID: "page-title"}, BookPropertyValues{
		Author: &TextPropertyValue{PropertyID: authorPropertyID, Value: "Alan"},
		ASIN:   &TextPropertyValue{PropertyID: asinPropertyID, Value: testASIN},
	})

	if got := ix.Match(book); got.Status != BookMatchExact || got.Book == nil || got.Book.ID != "page-title" {
		t.Errorf("Match(book) = %+v, want exact page-title", got)
	}
	if got := ix.Match(kindleBook("", "Go", "Alan")); got.Status != BookMatchNone {
		t.Errorf("Match(other book with the same Title+Author).Status = %q, want %q", got.Status, BookMatchNone)
	}
}
