package main

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// 対象の選択のテストの時刻。Kindle の Last Annotated Date は UTC のその日の 0 時で、last_synced_at が 2 日以上後なら変化なしとみなす。
var (
	targetAnnotatedAt = time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	targetSyncedAt    = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
)

// targetBooks は Last Annotated Date を持つ Book と持たない Book。
// Exact は記録済みで変化なし、New は記録なし、Ambiguous は Last Annotated Date なしで、記録の有無によらず対象になる。
func targetBooks() []domain.Book {
	exact, newBook := dryRunBookExact, dryRunBookNew
	exact.LastAnnotatedAt = &targetAnnotatedAt
	newBook.LastAnnotatedAt = &targetAnnotatedAt
	return []domain.Book{exact, newBook, dryRunBookAmbiguous}
}

// seedTargetState は state.db の books に、変化なしの Book と Last Annotated Date の無い Book の last_synced_at を記録する。
func seedTargetState(t *testing.T, p config.Paths) {
	t.Helper()
	s := state.NewSQLiteStateStore(p.StateFile())
	for _, id := range []string{dryRunBookExact.ID, dryRunBookAmbiguous.ID} {
		if err := s.MarkBookSynced(context.Background(), state.BookState{KindleBookID: id, DestinationBookID: "page-" + id, LastSyncedAt: targetSyncedAt}); err != nil {
			t.Fatal(err)
		}
	}
}

func newTargetBrowser() *fakeBrowser {
	b := newDryRunBrowser()
	b.books = targetBooks()
	return b
}

// highlightBookIDs は ListHighlights に渡った Book ID を順に返す。
func highlightBookIDs(b *fakeBrowser) []string {
	ids := make([]string, 0, len(b.hlBooks))
	for _, book := range b.hlBooks {
		ids = append(ids, book.ID)
	}
	return ids
}

func TestSyncTarget(t *testing.T) {
	wantSelected := []string{dryRunBookNew.ID, dryRunBookAmbiguous.ID}
	wantAll := []string{dryRunBookExact.ID, dryRunBookNew.ID, dryRunBookAmbiguous.ID}

	t.Run("通常の sync は変化なしと記録された Book の Highlight を取らず、記録の無い Book と Last Annotated Date の無い Book は同期し、対象の数を出す", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		seedTargetState(t, p)
		requests := stubWritableNotion(t)
		b := newTargetBrowser()
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got := highlightBookIDs(b); !slices.Equal(got, wantSelected) {
			t.Errorf("highlight book IDs = %q, want %q", got, wantSelected)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("2", "0", "2", "0", "0"), "2", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		// 変化なしの Book は Notion に要求を出さないので、書き込みは対象の 2 冊の分だけ
		if got, want := len(writeRequests(requests())), 2; got != want {
			t.Errorf("len(write requests) = %d, want %d", got, want)
		}
	})

	t.Run("sync --full は記録に関わらず全 Book の Highlight を取って同期する", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		seedTargetState(t, p)
		requests := stubWritableNotion(t)
		b := newTargetBrowser()
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t, "--full")
		if code != 0 || stderr != "" {
			t.Errorf("run(sync --full) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got := highlightBookIDs(b); !slices.Equal(got, wantAll) {
			t.Errorf("highlight book IDs = %q, want %q", got, wantAll)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("3", "0", "4", "0", "0"), "3", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		if got, want := len(writeRequests(requests())), 3; got != want {
			t.Errorf("len(write requests) = %d, want %d", got, want)
		}
	})

	t.Run("対象が無ければ Highlight も Notion への要求も出さない", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		seedTargetState(t, p)
		requests := stubWritableNotion(t)
		b := newTargetBrowser()
		b.books = b.books[:1] // 変化なしの Book だけ
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got := highlightBookIDs(b); len(got) != 0 {
			t.Errorf("highlight book IDs = %q, want none", got)
		}
		if got := requests(); len(got) != 0 {
			t.Errorf("notion requests = %v, want none", got)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("0", "0", "0", "0", "0"), "0", "1"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
	})

	t.Run("sync --dry-run の予定の表には通常の sync と同じ対象の Book だけが並び、対象の数を出す", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		seedTargetState(t, p)
		_, _ = setupDryRunNotion(t, dryRunReplies())
		b := newTargetBrowser()
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncDryRun(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync --dry-run) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		got := planCells(t, stdout)
		if titles := []string{got[1][0], got[2][0]}; !slices.Equal(titles, []string{dryRunBookNew.Title, dryRunBookAmbiguous.Title}) || got[3] == nil || len(got[3]) != 0 {
			t.Errorf("plan rows = %q, want only %q and %q", got[1:4], dryRunBookNew.Title, dryRunBookAmbiguous.Title)
		}
		if last, want := got[len(got)-1], []string{"Target books", "2 of 3"}; !slices.Equal(last, want) {
			t.Errorf("last row = %q, want %q", last, want)
		}
		// Last Annotated Date の無い Book は対象だが、照合で ambiguous になるので Highlight は取らない
		if got, want := highlightBookIDs(b), []string{dryRunBookNew.ID}; !slices.Equal(got, want) {
			t.Errorf("highlight book IDs = %q, want %q", got, want)
		}
	})

	t.Run("sync --dry-run --full は全 Book を予定の表に並べる", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		seedTargetState(t, p)
		_, _ = setupDryRunNotion(t, dryRunReplies())
		b := newTargetBrowser()
		stubBrowser(t, b, nil)

		var stdout, stderr bytes.Buffer
		if code := run([]string{"sync", "--dry-run", "--full"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
			t.Errorf("run(sync --dry-run --full) code = %d, stderr = %q, want 0 and empty", code, stderr.String())
		}
		got := planCells(t, stdout.String())
		if titles := []string{got[1][0], got[2][0], got[3][0]}; !slices.Equal(titles, []string{dryRunBookExact.Title, dryRunBookNew.Title, dryRunBookAmbiguous.Title}) {
			t.Errorf("plan titles = %q, want all books", titles)
		}
		if last, want := got[len(got)-1], []string{"Target books", "3 of 3"}; !slices.Equal(last, want) {
			t.Errorf("last row = %q, want %q", last, want)
		}
	})

	t.Run("sync --dry-run は state.db が無ければ作らず、あっても内容を変えない", func(t *testing.T) {
		t.Run("state.db が無ければ作らない", func(t *testing.T) {
			p := setupXDG(t, validConfigTOML)
			_, _ = setupDryRunNotion(t, dryRunReplies())
			b := newTargetBrowser()
			stubBrowser(t, b, nil)

			if code, _, stderr := runSyncDryRun(t); code != 0 {
				t.Errorf("run(sync --dry-run) code = %d, stderr = %q, want 0", code, stderr)
			}
			if got, want := dataDirEntries(t, p.DataDir), []string{"run.lock"}; !slices.Equal(got, want) {
				t.Errorf("data dir entries = %v, want %v", got, want)
			}
		})

		t.Run("state.db があっても内容を変えない", func(t *testing.T) {
			p := setupSyncedXDG(t, validConfigTOML)
			seedTargetState(t, p)
			_, _ = setupDryRunNotion(t, dryRunReplies())
			stubBrowser(t, newTargetBrowser(), nil)
			before, err := os.ReadFile(p.StateFile())
			if err != nil {
				t.Fatal(err)
			}

			if code, _, stderr := runSyncDryRun(t); code != 0 {
				t.Errorf("run(sync --dry-run) code = %d, stderr = %q, want 0", code, stderr)
			}
			after, err := os.ReadFile(p.StateFile())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Error("state.db changed by sync --dry-run, want unchanged")
			}
		})
	})
}
