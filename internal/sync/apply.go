package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

// BookFailureKind は Book 1 冊の書き込みが失敗した種類。文言には Book ID とこの種類だけを出し、タイトル・token は入れない（§74）。
type BookFailureKind string

const (
	// BookFailureCreate は CreateBook が失敗した Book。
	BookFailureCreate BookFailureKind = "create failed"
	// BookFailureAdopt は AdoptBook が失敗した Book。
	BookFailureAdopt BookFailureKind = "adopt failed"
	// bookSkippedAmbiguous は照合が ambiguous で skip した Book の、partial failure の文言に出す種類（§51）。
	bookSkippedAmbiguous = "ambiguous"
)

// BookFailure は書き込みに失敗した 1 冊の Book。
type BookFailure struct {
	BookID string
	Kind   BookFailureKind
}

// ApplyResult は ApplyBooks の結果。各 slice は Book ID を入力の順に持ち、件数は len で取る。
type ApplyResult struct {
	Created []string      // CreateBook でページを作った Book
	Adopted []string      // AdoptBook で既存ページを引き取った Book
	Exact   []string      // ページがあり何も書かなかった Book
	Skipped []string      // 照合が ambiguous で skip した Book（§51）
	Failed  []BookFailure // 書き込みが Book 単位で失敗した Book（§72）
}

// PartialFailure は skip か失敗が 1 件以上あれば exitcode.PartialFailure（8）の error を返し、無ければ nil を返す（§72）。
// 文言は Book ID と種類だけを並べる。
func (r ApplyResult) PartialFailure() error {
	if len(r.Skipped) == 0 && len(r.Failed) == 0 {
		return nil
	}
	return exitcode.New(exitcode.PartialFailure, &booksNotSyncedError{skipped: r.Skipped, failed: r.Failed})
}

// booksNotSyncedError は同期先に反映できなかった Book を集めたもの。
type booksNotSyncedError struct {
	skipped []string
	failed  []BookFailure
}

func (e *booksNotSyncedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sync: %d books were not synced:", len(e.skipped)+len(e.failed))
	sep := " "
	for _, id := range e.skipped {
		fmt.Fprintf(&b, "%s%s (%s)", sep, id, bookSkippedAmbiguous)
		sep = "; "
	}
	for _, f := range e.failed {
		fmt.Fprintf(&b, "%s%s (%s)", sep, f.BookID, f.Kind)
		sep = "; "
	}
	return b.String()
}

// ApplyBooks は Book ごとに照合し、none なら CreateBook、adoptable なら AdoptBook を呼び、exact なら何も書かず、ambiguous は skip する（§51）。
// 1 冊の書き込みが 400 / 5xx / タイムアウトで失敗してもその Book を Failed に残して残りを続ける（§72）。
// タイムアウトで作成の結果が分からない Book は再送しない（§29。次の sync の照合で作成済みなら exact になる）。
// 書き込みが 401 / 403 / 404 で失敗したとき（§73）と FindBook が失敗したときは、その時点で残りを書かずに exitcode.Notion の error を返す。
// そのときの ApplyResult には止まるまでの Book が入っている。
// 返した error が nil でも skip / 失敗はあり得るので、呼び出し側は PartialFailure で確かめる。
func ApplyBooks(ctx context.Context, books []domain.Book, dst destination.Destination) (ApplyResult, error) {
	var r ApplyResult
	for _, b := range books {
		m, err := dst.FindBook(ctx, b)
		if err != nil {
			return r, exitcode.New(exitcode.Notion, fmt.Errorf("sync: find book %s: %w", b.ID, err))
		}
		switch m.Status {
		case destination.BookMatchExact:
			r.Exact = append(r.Exact, b.ID)
		case destination.BookMatchAdoptable:
			if err := dst.AdoptBook(ctx, *m.Book, b); err != nil {
				if err := r.fail(b.ID, BookFailureAdopt, err); err != nil {
					return r, err
				}
				continue
			}
			r.Adopted = append(r.Adopted, b.ID)
		case destination.BookMatchNone:
			if _, err := dst.CreateBook(ctx, b); err != nil {
				if err := r.fail(b.ID, BookFailureCreate, err); err != nil {
					return r, err
				}
				continue
			}
			r.Created = append(r.Created, b.ID)
		case destination.BookMatchAmbiguous:
			r.Skipped = append(r.Skipped, b.ID)
		}
	}
	return r, nil
}

// fail は書き込みの失敗を仕分ける。fatal（§73）なら exitcode.Notion の error を返し、そうでなければ Failed に足して nil を返す。
func (r *ApplyResult) fail(bookID string, kind BookFailureKind, err error) error {
	if isFatalWrite(err) {
		return exitcode.New(exitcode.Notion, fmt.Errorf("sync: %s %s: %w", kind, bookID, err))
	}
	r.Failed = append(r.Failed, BookFailure{BookID: bookID, Kind: kind})
	return nil
}

// isFatalWrite は Notion の認証の失敗（401 / 403）と Data Source・ページに届かない失敗（404）を fatal とみなす（§73）。
// 400 / 5xx / タイムアウトなど、それ以外は Book 単位の失敗として続ける（§72）。
func isFatalWrite(err error) bool {
	var status *notion.StatusError
	if !errors.As(err, &status) {
		return false
	}
	switch status.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}
