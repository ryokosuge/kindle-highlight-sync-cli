package main

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// dedupBooksByPath は dedupReplies の 3 冊を、ページの子 block を読む path で引く。
var dedupBooksByPath = map[string]domain.Book{
	testDryRunBlocksPath:       dryRunBookExact,
	testCreatedBlocksPath:      dryRunBookNew,
	testInitialAdoptBlocksPath: initialBookAdopt,
}

// dedupReplies は 3 冊とも exact と照合され、子 block が空（exact は 1 件目の Sync Marker だけ）の Notion の応答。
// 追記は dryRunNotionReply が覚えるので、続く sync の GET children に追記済みの block が返る。
func dedupReplies() map[string]string {
	replies := dryRunReplies()
	replies[testDryRunQueryPath] = queryBody(
		dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
		dryRunPageJSON("page-created", dryRunBookNew.ID, dryRunBookNew.Title),
		dryRunPageJSON("page-adopt", initialBookAdopt.ID, initialBookAdopt.Title),
	)
	replies[testCreatedBlocksPath] = blockListBody()
	replies[testInitialAdoptBlocksPath] = blockListBody()
	replies[testPagesPath+"/page-created"] = `{"object":"page","id":"page-created"}`
	replies[testPagesPath+"/page-adopt"] = `{"object":"page","id":"page-adopt"}`
	return replies
}

// savedHighlights は state.db の highlights を Book ID ごとに返す。
func savedHighlights(t *testing.T, p config.Paths) map[string]map[string]state.HighlightState {
	t.Helper()
	s := state.NewSQLiteStateStore(p.StateFile())
	got := map[string]map[string]state.HighlightState{}
	for _, b := range dedupBooksByPath {
		states, err := s.ListHighlightIDs(context.Background(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		got[b.ID] = states
	}
	return got
}

// highlightIDs は states の KindleHighlightID を昇順で返す。
func highlightIDs(states map[string]state.HighlightState) []string {
	return slices.Sorted(maps.Keys(states))
}

func TestSyncDedup(t *testing.T) {
	// 1 回目の sync のあとの State が、追記した Highlight と Notion の既知を Block ID 付きで持つこと
	wantSavedIDs := map[string][]string{
		dryRunBookExact.ID:  {"a-HIGHLIGHT-1", "a-NOTE-2"},
		dryRunBookNew.ID:    {"a-HIGHLIGHT-3"},
		initialBookAdopt.ID: {"a-HIGHLIGHT-5"},
	}
	assertSaved := func(t *testing.T, p config.Paths) {
		t.Helper()
		got := savedHighlights(t, p)
		for bookID, want := range wantSavedIDs {
			if ids := highlightIDs(got[bookID]); !slices.Equal(ids, want) {
				t.Errorf("saved highlight IDs of %s = %v, want %v", bookID, ids, want)
			}
			for id, h := range got[bookID] {
				if h.DestinationHighlightID == "" {
					t.Errorf("saved highlight %s of %s has empty Block ID", id, bookID)
				}
			}
		}
	}

	t.Run("通常の sync は追記した Highlight を Block ID 付きで state.db の highlights に残す", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, dedupReplies())
		stubBrowser(t, newInitialBrowser(), nil)

		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Fatalf("run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), applyCells("0", "0", "3", "0", "0"); !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		if got, want := len(appendRequests(requests())), 3; got != want {
			t.Errorf("len(append requests) = %d, want %d", got, want)
		}
		assertSaved(t, p)
	})

	t.Run("同じ Kindle の内容で続けて sync すると、2 回目は追記の要求を出さず追記件数が 0 になる", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, dedupReplies())
		stubBrowser(t, newInitialBrowser(), nil)
		if code, _, stderr := runSyncApply(t); code != 0 {
			t.Fatalf("1st run(sync) code = %d, stderr = %q, want 0", code, stderr)
		}
		firstRequests := len(requests())

		stubBrowser(t, newInitialBrowser(), nil)
		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("2nd run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), applyCells("0", "0", "0", "0", "0"); !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("2nd stdout cells = %q, want %q", got, want)
		}
		second := requests()[firstRequests:]
		if got := appendRequests(second); got != nil {
			t.Errorf("append requests in 2nd run = %q, want none", got)
		}
		// State の既知で足りるので、子 block の一覧も読まない
		for _, r := range second {
			if r.method == "GET" {
				t.Errorf("GET request in 2nd run = %q, want none", r.path)
			}
		}
	})

	t.Run("2 回目の前に Kindle の Highlight が 1 つ増えると、2 回目はその 1 つだけを追記する", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, dedupReplies())
		stubBrowser(t, newInitialBrowser(), nil)
		if code, _, stderr := runSyncApply(t); code != 0 {
			t.Fatalf("1st run(sync) code = %d, stderr = %q, want 0", code, stderr)
		}
		firstRequests := len(requests())

		b2 := newInitialBrowser()
		b2.highlights[initialBookAdopt.ID] = append(slices.Clone(b2.highlights[initialBookAdopt.ID]),
			domain.Highlight{ID: "a-HIGHLIGHT-6", BookID: initialBookAdopt.ID, Text: "Distinctive Highlight Six"})
		stubBrowser(t, b2, nil)
		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("2nd run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), applyCells("0", "0", "1", "0", "0"); !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("2nd stdout cells = %q, want %q", got, want)
		}
		if got, want := appendRequests(requests()[firstRequests:]), []string{"PATCH " + testInitialAdoptBlocksPath}; !slices.Equal(got, want) {
			t.Errorf("append requests in 2nd run = %q, want %q", got, want)
		}
		want := slices.Clone(wantSavedIDs[initialBookAdopt.ID])
		want = append(want, "a-HIGHLIGHT-6")
		if got := highlightIDs(savedHighlights(t, p)[initialBookAdopt.ID]); !slices.Equal(got, want) {
			t.Errorf("saved highlight IDs of %s = %v, want %v", initialBookAdopt.ID, got, want)
		}
	})

	t.Run("state.db を消して続けて sync しても追記は 0 回で、Notion の既知を state.db に保存し直す", func(t *testing.T) {
		p := setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, dedupReplies())
		stubBrowser(t, newInitialBrowser(), nil)
		if code, _, stderr := runSyncApply(t); code != 0 {
			t.Fatalf("1st run(sync) code = %d, stderr = %q, want 0", code, stderr)
		}
		firstRequests := len(requests())
		if err := os.Remove(p.StateFile()); err != nil {
			t.Fatal(err)
		}

		stubBrowser(t, newInitialBrowser(), nil)
		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("2nd run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), applyCells("0", "0", "0", "0", "0"); !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("2nd stdout cells = %q, want %q", got, want)
		}
		if got := appendRequests(requests()[firstRequests:]); got != nil {
			t.Errorf("append requests in 2nd run = %q, want none", got)
		}
		assertSaved(t, p)
	})

	t.Run("sync --dry-run は state.db を作らない", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		setupDryRunNotion(t, dedupReplies())
		stubBrowser(t, newInitialBrowser(), nil)

		if code, _, stderr := runSyncDryRun(t); code != 0 {
			t.Fatalf("run(sync --dry-run) code = %d, stderr = %q, want 0", code, stderr)
		}
		if _, err := os.Stat(p.StateFile()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stat(state.db) error = %v, want not exist", err)
		}
	})
}
