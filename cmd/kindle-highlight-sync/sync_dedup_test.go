package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// dedupBookIDs は newInitialBrowser の 3 冊の Book ID。
var dedupBookIDs = []string{dryRunBookExact.ID, dryRunBookNew.ID, initialBookAdopt.ID}

// dedupHighlight6 は 2 回目の前に Kindle で増える Highlight。
var dedupHighlight6 = domain.Highlight{ID: "a-HIGHLIGHT-6", BookID: initialBookAdopt.ID, Text: "Distinctive Highlight Six"}

// dedupSavedAfterFirstRun は 1 回目の sync のあとの highlights（Highlight ID → Block ID）。
// exact の a-HIGHLIGHT-1 は Notion の既知として読んだ block、ほかは fake の Notion が追記で返した block。
func dedupSavedAfterFirstRun() map[string]string {
	return map[string]string{
		"a-HIGHLIGHT-1": "block-1",
		"a-NOTE-2":      "appended-1",
		"a-HIGHLIGHT-3": "appended-1",
		"a-HIGHLIGHT-5": "appended-1",
	}
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

// savedBlockIDs は state.db の highlights を Highlight ID → Block ID で返す（Book をまたぐ読み取り順に依存しない）。
func savedBlockIDs(t *testing.T, p config.Paths) map[string]string {
	t.Helper()
	s := state.NewSQLiteStateStore(p.StateFile())
	got := map[string]string{}
	for _, bookID := range dedupBookIDs {
		states, err := s.ListHighlightIDs(context.Background(), bookID)
		if err != nil {
			t.Fatalf("ListHighlightIDs(%s) error = %v, want nil", bookID, err)
		}
		for id, h := range states {
			got[id] = h.DestinationHighlightID
		}
	}
	return got
}

func TestSyncDedup(t *testing.T) {
	// dedupRun は 1 回の sync の実行と、その結果の期待値。
	type dedupRun struct {
		args              []string
		extraHighlight    bool       // true なら実行前に Kindle の adoptable の Book に Highlight を 1 つ足す
		removeState       bool       // true なら実行前に state.db を消す
		wantCells         [][]string // nil なら標準出力の件数は見ない（dry-run）
		wantAppends       []string
		wantNoGetChildren bool // true なら子 block の一覧を読む GET が無い
	}
	tests := []struct {
		name string
		// true なら Initial Sync 済みの state.db を作らず、state.db が無い状態から始める
		noState bool
		runs    []dedupRun
		// 全 run のあとの highlights（Highlight ID → Block ID）。nil なら state.db が無い
		wantSaved map[string]string
	}{
		{
			name: "通常の sync は追記した Highlight を Block ID 付きで state.db の highlights に残す",
			runs: []dedupRun{
				{wantCells: applyCells("0", "0", "3", "0", "0"), wantAppends: []string{"PATCH " + testDryRunBlocksPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testInitialAdoptBlocksPath}},
			},
			wantSaved: dedupSavedAfterFirstRun(),
		},
		{
			name: "同じ Kindle の内容で続けて sync すると、2 回目は追記の要求を出さず追記件数が 0 になる",
			runs: []dedupRun{
				{wantCells: applyCells("0", "0", "3", "0", "0"), wantAppends: []string{"PATCH " + testDryRunBlocksPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testInitialAdoptBlocksPath}},
				// State の既知で足りるので、子 block の一覧も読まない
				{wantCells: applyCells("0", "0", "0", "0", "0"), wantNoGetChildren: true},
			},
			wantSaved: dedupSavedAfterFirstRun(),
		},
		{
			name: "2 回目の前に Kindle の Highlight が 1 つ増えると、2 回目はその 1 つだけを追記する",
			runs: []dedupRun{
				{wantCells: applyCells("0", "0", "3", "0", "0"), wantAppends: []string{"PATCH " + testDryRunBlocksPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testInitialAdoptBlocksPath}},
				{extraHighlight: true, wantCells: applyCells("0", "0", "1", "0", "0"), wantAppends: []string{"PATCH " + testInitialAdoptBlocksPath}},
			},
			wantSaved: func() map[string]string {
				saved := dedupSavedAfterFirstRun()
				saved[dedupHighlight6.ID] = "appended-2"
				return saved
			}(),
		},
		{
			name: "state.db を消して続けて sync しても追記は 0 回で、Notion の既知を state.db に保存し直す",
			runs: []dedupRun{
				{wantCells: applyCells("0", "0", "3", "0", "0"), wantAppends: []string{"PATCH " + testDryRunBlocksPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testInitialAdoptBlocksPath}},
				{removeState: true, wantCells: applyCells("0", "0", "0", "0", "0")},
			},
			wantSaved: dedupSavedAfterFirstRun(),
		},
		{
			name:    "sync --dry-run は state.db を作らない",
			noState: true,
			runs:    []dedupRun{{args: []string{"--dry-run"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p config.Paths
			if tt.noState {
				p = setupXDG(t, validConfigTOML)
			} else {
				p = setupSyncedXDG(t, validConfigTOML)
			}
			requests, _ := setupDryRunNotion(t, dedupReplies())
			for i, r := range tt.runs {
				if r.removeState {
					if err := os.Remove(p.StateFile()); err != nil {
						t.Fatal(err)
					}
				}
				b := newInitialBrowser()
				if r.extraHighlight {
					b.highlights[initialBookAdopt.ID] = append(slices.Clone(b.highlights[initialBookAdopt.ID]), dedupHighlight6)
				}
				stubBrowser(t, b, nil)
				before := len(requests())

				code, stdout, stderr := runSyncApply(t, r.args...)
				if code != 0 {
					t.Errorf("run %d: run(sync) = %d, want 0", i+1, code)
				}
				if stderr != "" {
					t.Errorf("run %d: stderr = %q, want %q", i+1, stderr, "")
				}
				if r.wantCells != nil {
					if got := planCells(t, stdout); !reflect.DeepEqual(got, r.wantCells) {
						t.Errorf("run %d: stdout cells = %q, want %q", i+1, got, r.wantCells)
					}
				}
				got := requests()[before:]
				if r.wantCells != nil && !slices.Equal(appendRequests(got), r.wantAppends) {
					t.Errorf("run %d: append requests = %q, want %q", i+1, appendRequests(got), r.wantAppends)
				}
				if r.wantNoGetChildren {
					for _, req := range got {
						if req.method == http.MethodGet {
							t.Errorf("run %d: GET request = %q, want none", i+1, req.path)
						}
					}
				}
			}
			if tt.wantSaved == nil {
				if _, err := os.Stat(p.StateFile()); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("stat(state.db) error = %v, want %v", err, os.ErrNotExist)
				}
				return
			}
			if got := savedBlockIDs(t, p); !reflect.DeepEqual(got, tt.wantSaved) {
				t.Errorf("saved Block IDs = %v, want %v", got, tt.wantSaved)
			}
		})
	}
}
