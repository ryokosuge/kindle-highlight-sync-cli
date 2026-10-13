package main

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

var (
	// recoverInterrupted は追記の後・保存の前に止まった Highlight（Notion に Sync Marker があり State に無い）、recoverNew はどちらにも無い Highlight。
	recoverInterrupted = domain.Highlight{ID: "a-INTERRUPTED-3", BookID: dryRunBookExact.ID, Text: "Distinctive Interrupted Highlight"}
	recoverNew         = domain.Highlight{ID: "a-NEW-4", BookID: dryRunBookExact.ID, Text: "Distinctive New Highlight"}
	// recoverAnnotatedAt は 1 回目の last_synced_at より後なので、2 回目以降も Book が対象になる。
	recoverAnnotatedAt = time.Now().AddDate(1, 0, 0).UTC().Truncate(24 * time.Hour)
)

// recoverAllHighlights は 2 回目以降の Kindle の Highlight（1 回目は先頭の 2 つ）。
func recoverAllHighlights() []domain.Highlight {
	return append(slices.Clone(dryRunHighlights[dryRunBookExact.ID]), recoverInterrupted, recoverNew)
}

// recoverBrowser は Last Annotated Date を持つ exact の Book だけを返す fake。
func recoverBrowser(highlights []domain.Highlight) *fakeBrowser {
	book := dryRunBookExact
	book.LastAnnotatedAt = &recoverAnnotatedAt
	b := newDryRunBrowser()
	b.books = []domain.Book{book}
	b.highlights = map[string][]domain.Highlight{book.ID: highlights}
	return b
}

func TestSyncRecoverInterrupt(t *testing.T) {
	// recoverRun は 1 回の sync の実行と、その結果の期待値。
	type recoverRun struct {
		interrupt       bool       // true なら実行前に、止まっていた Highlight の Quote Block を Notion の children に置き、Kindle の Highlight を増やす
		wantCells       [][]string // 標準出力の件数
		wantAppends     []string   // 追記の要求
		wantAppendedIDs []string   // 追記の body に Sync Marker がある Highlight ID
	}
	firstRun := recoverRun{wantCells: withTargetCount(applyCells("0", "0", "1", "0", "0"), "1", "1"), wantAppends: []string{"PATCH " + testDryRunBlocksPath}, wantAppendedIDs: []string{"a-NOTE-2"}}
	// 止まっていた Highlight は追記せず、新しい Highlight だけを追記する
	recoverSecondRun := recoverRun{interrupt: true, wantCells: withTargetCount(applyCells("0", "0", "1", "0", "0"), "1", "1"), wantAppends: []string{"PATCH " + testDryRunBlocksPath}, wantAppendedIDs: []string{recoverNew.ID}}
	noAppendRun := recoverRun{wantCells: withTargetCount(applyCells("0", "0", "0", "0", "0"), "1", "1")}

	tests := []struct {
		name string
		runs []recoverRun
		// 全 run のあとの highlights（Highlight ID → Block ID）。nil なら見ない
		wantSaved map[string]string
		// true なら全 run のあと、どの Highlight ID の Sync Marker もページに 1 つだけである
		wantSingleMarkers bool
	}{
		{
			name: "Notion に Sync Marker がある Highlight は追記せず、どちらにも無い Highlight だけを追記する",
			runs: []recoverRun{firstRun, recoverSecondRun},
		},
		{
			name: "止まっていた Highlight も新しい Highlight も Block ID 付きで state.db の highlights に残る",
			runs: []recoverRun{firstRun, recoverSecondRun},
			wantSaved: map[string]string{
				"a-HIGHLIGHT-1":       "block-1",
				"a-NOTE-2":            "appended-1",
				recoverInterrupted.ID: "block-interrupted",
				recoverNew.ID:         "appended-2",
			},
		},
		{
			name:              "続けて sync すると追記の要求は 0 回で、どの Highlight ID の Sync Marker もページに 1 つだけである",
			runs:              []recoverRun{firstRun, recoverSecondRun, noAppendRun},
			wantSingleMarkers: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupSyncedXDG(t, validConfigTOML)
			replies := dryRunReplies()
			// 追記の要求の body を記録する Notion の代役
			keyring.MockInit()
			t.Setenv(secrets.EnvNotionToken, testDryRunToken)
			reply := dryRunNotionReply(t, replies)
			var appendBodies []string
			srv, requests := newNotionServerHandler(t, func(method, path string, body []byte) string {
				if method == http.MethodPatch && path == testDryRunBlocksPath {
					appendBodies = append(appendBodies, string(body))
				}
				return reply(method, path, body)
			})
			stubNewNotionClient(t, srv)

			exactHighlights := dryRunHighlights[dryRunBookExact.ID]
			var initialBlocksReply string
			for i, r := range tt.runs {
				highlights := exactHighlights
				if r.interrupt || i > 0 {
					highlights = recoverAllHighlights()
				}
				if r.interrupt {
					// 追記の後・保存の前に止まった状態: Notion には Sync Marker があり、State の highlights には無い
					replies[testDryRunBlocksPath] = blockListBody(
						dryRunBlockJSON("block-1", exactHighlights[0].Text+"\nkhs:"+exactHighlights[0].ID),
						dryRunBlockJSON("block-interrupted", recoverInterrupted.Text+"\nkhs:"+recoverInterrupted.ID),
					)
					initialBlocksReply = replies[testDryRunBlocksPath]
				}
				b := recoverBrowser(highlights)
				stubBrowser(t, b, nil)
				before, bodiesBefore := len(requests()), len(appendBodies)

				code, stdout, stderr := runSyncApply(t)
				if code != 0 || stderr != "" {
					t.Fatalf("run %d: run(sync) = %d, stderr = %q, want 0 and empty", i+1, code, stderr)
				}
				if got := highlightBookIDs(b); !slices.Equal(got, []string{dryRunBookExact.ID}) {
					t.Errorf("run %d: highlight book IDs = %q, want %q", i+1, got, []string{dryRunBookExact.ID})
				}
				if got := planCells(t, stdout); !reflect.DeepEqual(got, r.wantCells) {
					t.Errorf("run %d: stdout cells = %q, want %q", i+1, got, r.wantCells)
				}
				if got := appendRequests(requests()[before:]); !slices.Equal(got, r.wantAppends) {
					t.Errorf("run %d: append requests = %q, want %q", i+1, got, r.wantAppends)
				}
				var appendedIDs []string
				for _, body := range appendBodies[bodiesBefore:] {
					for _, h := range recoverAllHighlights() {
						if strings.Contains(body, "khs:"+h.ID) {
							appendedIDs = append(appendedIDs, h.ID)
						}
					}
				}
				if !slices.Equal(appendedIDs, r.wantAppendedIDs) {
					t.Errorf("run %d: appended Highlight IDs = %q, want %q", i+1, appendedIDs, r.wantAppendedIDs)
				}
			}

			if tt.wantSaved != nil {
				states, err := state.NewSQLiteStateStore(p.StateFile()).ListHighlightIDs(context.Background(), dryRunBookExact.ID)
				if err != nil {
					t.Fatalf("ListHighlightIDs error = %v, want nil", err)
				}
				saved := map[string]string{}
				for id, h := range states {
					saved[id] = h.DestinationHighlightID
				}
				if !reflect.DeepEqual(saved, tt.wantSaved) {
					t.Errorf("saved Block IDs = %v, want %v", saved, tt.wantSaved)
				}
			}
			if tt.wantSingleMarkers {
				// ページの Sync Marker は、初めからある block と追記の要求の body の合計
				page := initialBlocksReply + strings.Join(appendBodies, "")
				for _, h := range recoverAllHighlights() {
					if got := strings.Count(page, "khs:"+h.ID); got != 1 {
						t.Errorf("Sync Marker count of %s = %d, want 1", h.ID, got)
					}
				}
			}
		})
	}
}
