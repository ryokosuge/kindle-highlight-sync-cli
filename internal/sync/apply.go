package sync

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// highlightStore は State から Book の既知の Highlight を読み、Notion で見つけた分と追記した分を保存し、追記の残りが 0 で終わった Book を記録する。state.StateStore の一部で、テストで差し替える。
type highlightStore interface {
	ListHighlightIDs(ctx context.Context, bookID string) (map[string]state.HighlightState, error)
	SaveHighlights(ctx context.Context, states []state.HighlightState) error
	MarkBookSynced(ctx context.Context, b state.BookState) error
}

// ApplyResult は ApplyBooks の結果。各 slice は Book ID を入力の順に持ち、件数は len で取る。
type ApplyResult struct {
	Created []string     // CreateBook でページを作った Book
	Adopted []string     // AdoptBook で既存ページを引き取った Book
	Exact   []string     // ページがあり UpdateBook で更新できた Book。追記の失敗は Failed に独立して載る
	Skipped []string     // 照合が ambiguous で skip した Book（§51）
	Failed  []FailedBook // 書き込みが Book 単位で失敗した Book（§72）

	Refound   []string // CreateBook の結果が分からず、Books Database を照合し直した Book
	Recovered []string // 照合し直しで見つかった作成済みのページを使った Book。Created には載せない

	Appended int // 追記できた Highlight の数。一部だけ成功した Book の成功分と、結果不明のあと確かめ直しで Notion に在ると分かった分も数える

	Rechecked int // 追記の結果が不明で、Notion の Highlight を取り直して確かめた Book の数
	Resent    int // 確かめ直しで未反映と分かり、AppendHighlights に送り直した Highlight の数
}

const (
	// maxRecheck は 1 冊の追記の結果が分からないときの、確かめ直しと再送の上限回数（§29）。
	maxRecheck = 2
	// recheckBaseBackoff は 1 回目の確かめ直しの前の待ち。以後は倍々にする。
	recheckBaseBackoff = time.Second
	// maxRecheckRetryAfter は Retry-After に従って待つ長さの上限。超えるときは待たずにその Book を失敗にする。
	maxRecheckRetryAfter = 60 * time.Second
)

// recheckSleep と recheckJitter は確かめ直しの前の待ちで、テストで差し替える。
var (
	recheckSleep  = sleepContext
	recheckJitter = func() time.Duration { return rand.N(time.Second) }
)

// recheckDelay は n 回目（1 始まり）の確かめ直しの前に待つ長さを返す。
// 429 で Retry-After があればその長さ（上限を超えるなら ok = false）、無いときと 429 以外は 1 秒・2 秒に jitter を足した長さ。
func recheckDelay(n int, err error) (d time.Duration, ok bool) {
	if after, has := notion.RetryAfter(err); has {
		return after, after <= maxRecheckRetryAfter
	}
	return recheckBaseBackoff<<(n-1) + recheckJitter(), true
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// PartialFailure は skip か失敗が 1 件以上あれば exitcode.PartialFailure（8）の error を返し、無ければ nil を返す（§72）。
// 文言は Book ID と種類だけを並べる。
func (r ApplyResult) PartialFailure() error {
	if len(r.Skipped) == 0 && len(r.Failed) == 0 {
		return nil
	}
	failed := make([]FailedBook, 0, len(r.Skipped)+len(r.Failed))
	for _, id := range r.Skipped {
		failed = append(failed, FailedBook{BookID: id, Kind: FailureAmbiguous})
	}
	return NewFailedBooksError(append(failed, r.Failed...))
}

// ApplyBooks は Book ごとに照合し、none なら CreateBook、adoptable なら AdoptBook、exact なら UpdateBook を呼び、ambiguous は skip する（§51）。
// ページが決まった Book（none は作成後、adoptable は Adopt 後、exact は UpdateBook 後）は、Source の Highlight を取り、
// State の既知に無い Highlight（Candidates）があるときだけ Notion の既知（ListHighlightIDs）を取って、さらに既知を除いた残りを Source から取った順のまま AppendHighlights に渡す（§24 / §26）。
// State の既知で足りる Book は Notion の一覧も追記も呼ばない。none の Book は、作成したページも照合し直しで見つけたページも、この実行で作った（見つけた）もので State が知る Highlight を持たないので、State も Notion の一覧も見ず全部を渡す。
// Notion にあり State に無い Highlight（backfill）と追記できた Highlight は Block ID 付きで Book ごとに 1 回 SaveHighlights する。now は LastSyncedAt に入れる。
// 追記が失敗しても、成功した分を保存してから失敗を返す。State の読み取り・保存の失敗は、残りの Book を書かずに exitcode.LocalState の error を返す。
// Highlight が 0 件の Book は State も Notion の一覧も読まず、追記するものが無い Book では AppendHighlights を呼ばない。
// 取得の順は dry-run（BuildPlan。既知 → Highlight）と違うが、既知を除く判定（unknownHighlights）は共有している。
// ASIN の無い Book は照合も書き込みも取得もせず FailureNoASIN で Failed に残して残りを続ける。
// 1 冊の書き込み・既知の取得が 400 / 429 / 5xx / タイムアウトで失敗しても、Highlight の取得が 1 冊だけの構造の異常か Highlight ID が作れない error で失敗しても、その Book を Failed に残して残りを続ける（§72）。
// CreateBook が結果の分からない失敗（notion.OutcomeUnknown）のときは、いきなり再送せず refindCreated で照合し直す（§29）。
// 追記の結果が分からない失敗（notion.OutcomeUnknown）は、待ってから Notion の Highlight を取り直し、未反映の分だけを 2 回まで送り直す（appendWithRecheck）。
// 書き込みが 401 / 403 / 404 で失敗したとき（§73）と FindBook が失敗したときは、その時点で残りを書かずに exitcode.Notion の error を返す。
// 書き込みが context のキャンセルで失敗したときも残りを書かずに止め、元の error を包まずにそのまま返す。
// どちらのときも ApplyResult には止まるまでの Book と追記できた件数が入っている。
// 返した error が nil でも skip / 失敗はあり得るので、呼び出し側は PartialFailure で確かめる。
func ApplyBooks(ctx context.Context, books []domain.Book, src highlightLister, dst destination.Destination, st highlightStore, now time.Time) (ApplyResult, error) {
	var r ApplyResult
	for _, b := range books {
		if b.ASIN == "" {
			// Highlight のページは ASIN でしか開けない。黙って飛ばすと取りこぼしに気づけないので、照合も書き込みもせず失敗に数える
			r.Failed = append(r.Failed, FailedBook{BookID: b.ID, Kind: FailureNoASIN})
			continue
		}
		m, err := dst.FindBook(ctx, b)
		if err != nil {
			return r, exitcode.New(exitcode.Notion, fmt.Errorf("sync: find book %s: %w", b.ID, err))
		}
		switch m.Status {
		case destination.BookMatchExact:
			if err := dst.UpdateBook(ctx, *m.Book, b); err != nil {
				if err := r.fail(b.ID, FailureNotionWrite, err); err != nil {
					return r, err
				}
				continue
			}
			r.Exact = append(r.Exact, b.ID)
			if err := r.syncHighlights(ctx, b, *m.Book, true, src, dst, st, now); err != nil {
				return r, err
			}
		case destination.BookMatchAdoptable:
			if err := dst.AdoptBook(ctx, *m.Book, b); err != nil {
				if err := r.fail(b.ID, FailureNotionWrite, err); err != nil {
					return r, err
				}
				continue
			}
			r.Adopted = append(r.Adopted, b.ID)
			if err := r.syncHighlights(ctx, b, *m.Book, true, src, dst, st, now); err != nil {
				return r, err
			}
		case destination.BookMatchNone:
			remote, err := dst.CreateBook(ctx, b)
			created := true
			if err != nil && notion.OutcomeUnknown(err) {
				remote, created, err = r.refindCreated(ctx, b, err, dst)
			}
			if err != nil {
				if err := r.fail(b.ID, FailureNotionWrite, err); err != nil {
					return r, err
				}
				continue
			}
			if created {
				r.Created = append(r.Created, b.ID)
			}
			if err := r.syncHighlights(ctx, b, remote, false, src, dst, st, now); err != nil {
				return r, err
			}
		case destination.BookMatchAmbiguous:
			r.Skipped = append(r.Skipped, b.ID)
		}
	}
	return r, nil
}

// refindCreated は結果の分からない CreateBook の失敗 createErr のあと、Books Database を照合し直して使うページを決める。
// exact なら作成済みのページを Recovered に数えて返し（created = false）、none なら待ってもう 1 回照合し直して、それでも none のときだけ CreateBook を 1 回呼び直す。
// 作り直しの失敗は照合し直さずそのまま返す。ambiguous・adoptable・照合し直しの失敗は、CreateBook も AdoptBook も呼ばずに error で返す（呼び出し側が Failed に仕分ける）。
func (r *ApplyResult) refindCreated(ctx context.Context, book domain.Book, createErr error, dst destination.Destination) (remote destination.RemoteBook, created bool, err error) {
	r.Refound = append(r.Refound, book.ID)
	for i := range 2 {
		if i > 0 {
			d, ok := recheckDelay(i, createErr)
			if !ok {
				return destination.RemoteBook{}, false, createErr
			}
			if err := recheckSleep(ctx, d); err != nil {
				return destination.RemoteBook{}, false, err
			}
		}
		m, err := dst.RefindBook(ctx, book)
		if err != nil {
			return destination.RemoteBook{}, false, err
		}
		switch m.Status {
		case destination.BookMatchExact:
			r.Recovered = append(r.Recovered, book.ID)
			return *m.Book, false, nil
		case destination.BookMatchNone:
			continue
		default:
			return destination.RemoteBook{}, false, createErr
		}
	}
	remote, err = dst.CreateBook(ctx, book)
	return remote, true, err
}

// syncHighlights は appendUnknown で追記し、追記の残りが 0 で終わった Book だけを books に now で記録する。
// 記録の失敗は、残りの Book を書かずに exitcode.LocalState の error を返す。記録されなかった Book の前回の記録は変えない。
func (r *ApplyResult) syncHighlights(ctx context.Context, book domain.Book, remote destination.RemoteBook, hasKnown bool, src highlightLister, dst destination.Destination, st highlightStore, now time.Time) error {
	done, err := r.appendUnknown(ctx, book, remote, hasKnown, src, dst, st, now)
	if err != nil || !done {
		return err
	}
	// 追記の失敗と同じく、中断・タイムアウトでも記録できるよう、キャンセルだけ切り離した context で記録する
	synced := state.BookState{KindleBookID: book.ID, DestinationBookID: remote.ID, ASIN: book.ASIN, Title: book.Title, LastAnnotatedAt: book.LastAnnotatedAt, LastSyncedAt: now}
	if err := st.MarkBookSynced(context.WithoutCancel(ctx), synced); err != nil {
		return localStateError("mark book synced", book.ID, err)
	}
	return nil
}

// appendUnknown は remote のページに、State にも Notion にも無い Highlight を追記して件数を r.Appended に足し、Notion で見つけた分と追記した分を State に保存する。
// Highlight が 0 件なら State も既知も取らずに返す。hasKnown が false のページ（作成直後）は State も Notion の既知も取らず、Source の Highlight を全部渡す。
// Book 単位の失敗は r.Failed に残して nil を返し、止めるべき失敗（fail 参照）と State の失敗は error で返す。
// 戻り値の bool は、失敗が無く追記の残りが 0 で終わった（追記するものが無かった場合を含む）ことを表す。
func (r *ApplyResult) appendUnknown(ctx context.Context, book domain.Book, remote destination.RemoteBook, hasKnown bool, src highlightLister, dst destination.Destination, st highlightStore, now time.Time) (bool, error) {
	hs, err := src.ListHighlights(ctx, book)
	if err != nil {
		kind, ok := ClassifyFetchError(err)
		if !ok {
			return false, err
		}
		return false, r.fail(book.ID, kind, err)
	}
	if len(hs) == 0 {
		return true, nil
	}
	var local map[string]state.HighlightState
	if hasKnown {
		if local, err = st.ListHighlightIDs(ctx, book.ID); err != nil {
			return false, localStateError("list highlights", book.ID, err)
		}
	}
	candidates := Candidates(hs, local)
	if len(candidates) == 0 {
		return true, nil
	}
	var known map[string]destination.RemoteHighlight
	if hasKnown {
		if known, err = dst.ListHighlightIDs(ctx, remote); err != nil {
			return false, r.fail(book.ID, FailureNotionWrite, err)
		}
	}
	unknown, backfill := Dedup(candidates, known)
	var appended []destination.RemoteHighlight
	var appendErr error
	if len(unknown) > 0 {
		appended, appendErr = r.appendWithRecheck(ctx, remote, unknown, dst)
	}
	// 追記が途中で失敗しても、成功した分は次の sync で重複して書かないよう先に保存する。
	// 中断・タイムアウトでの失敗でも保存できるよう、キャンセルだけ切り離した context で保存し、元の追記の error は fail で仕分ける
	if saved := highlightStates(book.ID, append(backfill, appended...), now); len(saved) > 0 {
		if err := st.SaveHighlights(context.WithoutCancel(ctx), saved); err != nil {
			return false, localStateError("save highlights", book.ID, err)
		}
	}
	if appendErr != nil {
		return false, r.fail(book.ID, FailureNotionWrite, appendErr)
	}
	return true, nil
}

// appendWithRecheck は unknown を AppendHighlights に渡し、結果が分からない失敗（notion.OutcomeUnknown）のときは待ってから Notion の Highlight を取り直し、
// 送ったのに成功に数えていない Highlight のうち Notion に在るものは成功分に足し、無いものだけを元の順のまま送り直す。確かめ直しと再送は maxRecheck 回まで。
// 戻り値は成功に数えてよい分（AppendHighlights が返した分と、確かめ直しで Notion に在った分）と、失敗したときの error。
func (r *ApplyResult) appendWithRecheck(ctx context.Context, remote destination.RemoteBook, unknown []domain.Highlight, dst destination.Destination) ([]destination.RemoteHighlight, error) {
	confirmed, err := dst.AppendHighlights(ctx, remote, unknown)
	r.Appended += len(confirmed)
	pending := unknownHighlights(unknown, remoteByHighlightID(confirmed))
	rechecked := false
	for n := 1; err != nil && notion.OutcomeUnknown(err) && n <= maxRecheck; n++ {
		d, ok := recheckDelay(n, err)
		if !ok {
			break
		}
		if serr := recheckSleep(ctx, d); serr != nil {
			return confirmed, serr
		}
		if !rechecked {
			rechecked = true
			r.Rechecked++
		}
		known, lerr := dst.ListHighlightIDs(ctx, remote)
		if lerr != nil {
			return confirmed, lerr
		}
		for _, h := range pending {
			if rh, ok := known[h.ID]; ok {
				confirmed = append(confirmed, rh)
				r.Appended++
			}
		}
		pending = unknownHighlights(pending, known)
		if len(pending) == 0 {
			return confirmed, nil
		}
		r.Resent += len(pending)
		var resent []destination.RemoteHighlight
		resent, err = dst.AppendHighlights(ctx, remote, pending)
		confirmed = append(confirmed, resent...)
		r.Appended += len(resent)
		pending = unknownHighlights(pending, remoteByHighlightID(resent))
	}
	return confirmed, err
}

func remoteByHighlightID(rs []destination.RemoteHighlight) map[string]destination.RemoteHighlight {
	m := make(map[string]destination.RemoteHighlight, len(rs))
	for _, r := range rs {
		m[r.HighlightID] = r
	}
	return m
}

// localStateError は State の失敗を exitcode.LocalState（6）の error にする。すでに LocalState ならそのまま返す。
// context のキャンセルによる失敗は State の不整合ではなく中断なので、LocalState に変えず、種類を持たない中断の error にして返す
// （SQLite の State は中断も LocalState で包んで返す。中断時の exit code は呼び出し側が決める）。期限切れは State の失敗として LocalState に数える。
func localStateError(op, bookID string, err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("sync: %s %s: %w", op, bookID, context.Canceled)
	}
	if exitcode.Code(err) == int(exitcode.LocalState) {
		return err
	}
	return exitcode.New(exitcode.LocalState, fmt.Errorf("sync: %s %s: %w", op, bookID, err))
}

// fail は書き込みの失敗を仕分ける。context のキャンセルなら Book 単位の失敗でも fatal でもなく、元の error をそのまま返して止める
// （中断時の exit code は呼び出し側が決める）。fatal（§73）なら exitcode.Notion の error を返し、そうでなければ Failed に足して nil を返す。
// ローカル状態の不整合（exitcode.LocalState）も、種類を変えない元の error をそのまま返して止める。
// Highlight の取得の失敗の仕分けは ClassifyFetchError が行い、Book 単位でないもの（ブラウザの失敗・ErrHighlightsFrame・タイムアウトなど）は fail を通さず元の error をそのまま返して止める。
// 書き込みのタイムアウト（context.DeadlineExceeded）はキャンセルに含めず、Book 単位の失敗として続ける（§72）。
func (r *ApplyResult) fail(bookID string, kind FailureKind, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if isFatalWrite(err) {
		return exitcode.New(exitcode.Notion, fmt.Errorf("sync: %s %s: %w", kind, bookID, err))
	}
	// ローカル状態の不整合は Book 単位の一時的な失敗ではなく、続けると広がるので種類を保って止める
	if exitcode.Code(err) == int(exitcode.LocalState) {
		return err
	}
	r.Failed = append(r.Failed, FailedBook{BookID: bookID, Kind: kind})
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
