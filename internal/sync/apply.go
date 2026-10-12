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
	// BookFailureUpdate は UpdateBook が失敗した Book。
	BookFailureUpdate BookFailureKind = "update failed"
	// BookFailureListKnown は Notion の既知の Highlight の取得が失敗した Book。
	BookFailureListKnown BookFailureKind = "list highlights failed"
	// BookFailureFetch は Source からの Highlight の取得が失敗した Book。
	BookFailureFetch BookFailureKind = "fetch highlights failed"
	// BookFailureAppend は AppendHighlights が失敗した Book。一部だけ追記できていても失敗に数える。
	BookFailureAppend BookFailureKind = "append failed"
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
	Exact   []string      // ページがあり UpdateBook で更新できた Book。追記の失敗は Failed に独立して載る
	Skipped []string      // 照合が ambiguous で skip した Book（§51）
	Failed  []BookFailure // 書き込みが Book 単位で失敗した Book（§72）

	Appended int // 追記できた Highlight の数。一部だけ成功した Book の成功分も数える
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

// ApplyBooks は Book ごとに照合し、none なら CreateBook、adoptable なら AdoptBook、exact なら UpdateBook を呼び、ambiguous は skip する（§51）。
// ページが決まった Book（none は作成後、adoptable は Adopt 後、exact はそのまま）は、Source の Highlight を取り、
// 1 件でもあれば Notion の既知の Highlight（ListHighlightIDs）を取って、既知を除いた残りを Source から取った順のまま AppendHighlights に渡す（§24 / §26）。作成した Book はページが空なので既知を取らず全部を渡す。
// Highlight が 0 件の Book は Notion の一覧を読まず、追記するものが無い Book では AppendHighlights を呼ばない。
// 取得の順は dry-run（BuildPlan。既知 → Highlight）と違うが、既知を除く判定（unknownHighlights）は共有している。
// 1 冊の書き込み・Highlight の取得・既知の取得が 400 / 429 / 5xx / タイムアウトで失敗してもその Book を Failed に残して残りを続ける（§72）。
// タイムアウトで作成の結果が分からない Book は再送しない（§29。次の sync の照合で作成済みなら exact になる）。
// 書き込みが 401 / 403 / 404 で失敗したとき（§73）と FindBook が失敗したときは、その時点で残りを書かずに exitcode.Notion の error を返す。
// 書き込みが context のキャンセルで失敗したときも残りを書かずに止め、元の error を包まずにそのまま返す。
// どちらのときも ApplyResult には止まるまでの Book と追記できた件数が入っている。
// 返した error が nil でも skip / 失敗はあり得るので、呼び出し側は PartialFailure で確かめる。
func ApplyBooks(ctx context.Context, books []domain.Book, src highlightLister, dst destination.Destination) (ApplyResult, error) {
	var r ApplyResult
	for _, b := range books {
		m, err := dst.FindBook(ctx, b)
		if err != nil {
			return r, exitcode.New(exitcode.Notion, fmt.Errorf("sync: find book %s: %w", b.ID, err))
		}
		switch m.Status {
		case destination.BookMatchExact:
			if err := dst.UpdateBook(ctx, *m.Book, b); err != nil {
				if err := r.fail(b.ID, BookFailureUpdate, err); err != nil {
					return r, err
				}
				continue
			}
			r.Exact = append(r.Exact, b.ID)
			if err := r.appendUnknown(ctx, b, *m.Book, true, src, dst); err != nil {
				return r, err
			}
		case destination.BookMatchAdoptable:
			if err := dst.AdoptBook(ctx, *m.Book, b); err != nil {
				if err := r.fail(b.ID, BookFailureAdopt, err); err != nil {
					return r, err
				}
				continue
			}
			r.Adopted = append(r.Adopted, b.ID)
			if err := r.appendUnknown(ctx, b, *m.Book, true, src, dst); err != nil {
				return r, err
			}
		case destination.BookMatchNone:
			remote, err := dst.CreateBook(ctx, b)
			if err != nil {
				if err := r.fail(b.ID, BookFailureCreate, err); err != nil {
					return r, err
				}
				continue
			}
			r.Created = append(r.Created, b.ID)
			if err := r.appendUnknown(ctx, b, remote, false, src, dst); err != nil {
				return r, err
			}
		case destination.BookMatchAmbiguous:
			r.Skipped = append(r.Skipped, b.ID)
		}
	}
	return r, nil
}

// appendUnknown は remote のページに、既知に無い Highlight を追記して件数を r.Appended に足す。
// Highlight が 0 件なら既知も取らずに返す。hasKnown が false のページ（作成直後）は既知を取らず、Source の Highlight を全部渡す。
// Book 単位の失敗は r.Failed に残して nil を返し、止めるべき失敗（fail 参照）は error で返す。
func (r *ApplyResult) appendUnknown(ctx context.Context, book domain.Book, remote destination.RemoteBook, hasKnown bool, src highlightLister, dst destination.Destination) error {
	hs, err := src.ListHighlights(ctx, book)
	if err != nil {
		return r.fail(book.ID, BookFailureFetch, err)
	}
	if len(hs) == 0 {
		return nil
	}
	var known map[string]destination.RemoteHighlight
	if hasKnown {
		if known, err = dst.ListHighlightIDs(ctx, remote); err != nil {
			return r.fail(book.ID, BookFailureListKnown, err)
		}
	}
	unknown := unknownHighlights(hs, known)
	if len(unknown) == 0 {
		return nil
	}
	appended, err := dst.AppendHighlights(ctx, remote, unknown)
	r.Appended += len(appended)
	if err != nil {
		return r.fail(book.ID, BookFailureAppend, err)
	}
	return nil
}

// fail は書き込みの失敗を仕分ける。context のキャンセルなら Book 単位の失敗でも fatal でもなく、元の error をそのまま返して止める
// （中断時の exit code は呼び出し側が決める）。fatal（§73）なら exitcode.Notion の error を返し、そうでなければ Failed に足して nil を返す。
// タイムアウト（context.DeadlineExceeded）はキャンセルに含めず、Book 単位の失敗として続ける（§72）。
func (r *ApplyResult) fail(bookID string, kind BookFailureKind, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
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
