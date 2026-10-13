package sync

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

// FailureKind は Book 単位の失敗の種類。文言には Book ID とこの種類の名前だけを出し、タイトル・Highlight の本文・token は入れない（§74）。
type FailureKind int

const (
	// FailureNoASIN は ASIN が無く Highlight のページを開けない Book。
	FailureNoASIN FailureKind = iota + 1
	// FailureNotebookStructure は Book の Highlight のページだけが構造の異常な Book（§55 / §72）。
	FailureNotebookStructure
	// FailureHighlightID は行 id が無く Highlight ID が作れない Book（§16.1）。
	FailureHighlightID
	// FailureAmbiguous は照合が ambiguous で skip した Book（§51）。
	FailureAmbiguous
	// FailureNotionWrite は Notion への書き込みが失敗した Book。
	FailureNotionWrite
)

var failureKindNames = map[FailureKind]string{
	FailureNoASIN:            "book has no ASIN",
	FailureNotebookStructure: "unexpected notebook structure",
	FailureHighlightID:       "highlight ID cannot be made",
	FailureAmbiguous:         "ambiguous",
	FailureNotionWrite:       "notion write failed",
}

// String は文言に出す短い英語の名前を返す。
func (k FailureKind) String() string {
	return failureKindNames[k]
}

// FailedBook は Book 単位で失敗した 1 冊の Book。
type FailedBook struct {
	BookID string
	Kind   FailureKind
}

// NewFailedBooksError は失敗した Book の一覧から exitcode.PartialFailure（8）の error を作る。
// 文言は Book ID と種類の名前だけを並べる。failed は 1 件以上を渡す。
func NewFailedBooksError(failed []FailedBook) error {
	var b strings.Builder
	fmt.Fprintf(&b, "sync: %d books were not synced:", len(failed))
	sep := " "
	for _, f := range failed {
		fmt.Fprintf(&b, "%s%s (%s)", sep, f.BookID, f.Kind)
		sep = "; "
	}
	return exitcode.New(exitcode.PartialFailure, errors.New(b.String()))
}

// ClassifyFetchError は Highlight の取得の error を Book 単位の失敗に当てはめる。
// 当てはまれば種類と true を返し、当てはまらない（全 Book に共通の枠の異常・ctx の取消と期限切れ（context.DeadlineExceeded。run 全体の期限から来るので残りの Book も同じ失敗になる）・ブラウザ由来など、run を止める側の）error は false を返す。
// Highlight ID が作れない error は ErrSourceFormat も併記しているので、先に見る。
func ClassifyFetchError(err error) (FailureKind, bool) {
	switch {
	case errors.Is(err, domain.ErrMissingAnnotationID):
		return FailureHighlightID, true
	case errors.Is(err, source.ErrSourceFormat) && !errors.Is(err, source.ErrHighlightsFrame):
		return FailureNotebookStructure, true
	}
	return 0, false
}
