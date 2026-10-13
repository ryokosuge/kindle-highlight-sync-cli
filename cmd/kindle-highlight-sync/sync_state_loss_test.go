package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

const stateLossSecondBlocksPath = "/v1/blocks/page-second/children"

var (
	// 同期済みの 2 冊目。Notion には Kindle ID Property 入りのページと全 Highlight の Sync Marker がある
	stateLossBookSecond = domain.Book{ID: "B000000006", ASIN: "B000000006", Title: "Distinctive Second Book"}
	stateLossBooks      = []domain.Book{dryRunBookExact, stateLossBookSecond}
	stateLossHighlights = map[string][]domain.Highlight{
		dryRunBookExact.ID:     dryRunHighlights[dryRunBookExact.ID],
		stateLossBookSecond.ID: {{ID: "a-HIGHLIGHT-6", BookID: stateLossBookSecond.ID, Text: "Distinctive Highlight Six"}},
	}
	// 確認のための予定の算出が行う、Book の照合と Sync Marker の読み取りだけの要求
	stateLossReadRequests = []string{"POST " + testDryRunQueryPath, "GET " + testDryRunBlocksPath, "GET " + stateLossSecondBlocksPath}
)

// stateLossReplies は stateLossBooks の全 Highlight が Notion に Sync Marker 付きで在る応答。
func stateLossReplies() map[string]string {
	marker := func(blockID string, h domain.Highlight) string {
		return dryRunBlockJSON(blockID, h.Text+"\nkhs:"+h.ID)
	}
	first := stateLossHighlights[dryRunBookExact.ID]
	return map[string]string{
		testDryRunQueryPath: queryBody(
			dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
			dryRunPageJSON("page-second", stateLossBookSecond.ID, stateLossBookSecond.Title),
		),
		testDryRunBlocksPath:           blockListBody(marker("block-1", first[0]), dryRunBlockJSON("block-2", first[1].Note+"\nkhs:"+first[1].ID)),
		stateLossSecondBlocksPath:      blockListBody(marker("block-3", stateLossHighlights[stateLossBookSecond.ID][0])),
		testPagesPath + "/page-exact":  `{"object":"page","id":"page-exact"}`,
		testPagesPath + "/page-second": `{"object":"page","id":"page-second"}`,
	}
}

// newStateLossBrowser は Notebook に到達し books とその Highlight を返す fake。
func newStateLossBrowser(books []domain.Book) *fakeBrowser {
	b := newDryRunBrowser()
	b.books = books
	b.highlights = stateLossHighlights
	return b
}

// stateLossSyncOnce は 1 回目の sync を --yes 付きで走らせて同期済みにし、state.db を消す。
func stateLossSyncOnce(t *testing.T, p config.Paths) {
	t.Helper()
	stubBrowser(t, newStateLossBrowser(stateLossBooks), nil)
	if code, _, stderr := runSyncApply(t, "--yes"); code != 0 {
		t.Fatalf("run(sync --yes) code = %d, stderr = %q, want 0", code, stderr)
	}
	if !initialSyncRecorded(t, p) {
		t.Fatal("initial_sync_completed_at recorded = false after the first sync, want true")
	}
	if err := os.Remove(filepath.Join(p.DataDir, "state.db")); err != nil {
		t.Fatalf("os.Remove(state.db) error = %v, want nil", err)
	}
}

// stateLossApplyRequests は消した後の sync が確認なしで進んだときの要求。予定の算出の読み取りのあと、exact のページごとに Property を更新して既知を読み、追記も作成もしない。
var stateLossApplyRequests = slices.Concat(stateLossReadRequests, []string{"POST " + testDryRunQueryPath,
	"PATCH " + testPagesPath + "/page-exact", "GET " + testDryRunBlocksPath,
	"PATCH " + testPagesPath + "/page-second", "GET " + stateLossSecondBlocksPath})

// wantStateLossNotConfirmedErr は Kindle にだけ在る Book 1 冊（Create 1 件・Adopt 0 件）で確認が要るときの標準エラー。
const wantStateLossNotConfirmedErr = "configuration / confirmation required: initial sync will create 1 books and adopt 0 existing pages: run sync with --yes to confirm without a terminal\n"

func TestSyncStateLoss(t *testing.T) {
	tests := []struct {
		name         string
		books        []domain.Book
		wantCode     int
		wantStderr   string
		wantCells    [][]string // nil なら標準出力の件数は確かめない
		wantRequests []string
		wantRecorded bool
	}{
		{
			name:         "state.db を消した後の sync は確認なしで既存のページに照合し、追記 0 件で State を作り直す",
			books:        stateLossBooks,
			wantCode:     0,
			wantCells:    withTargetCount(applyCells("0", "0", "0", "0", "0"), "2", "2"),
			wantRequests: stateLossApplyRequests,
			wantRecorded: true,
		},
		{
			name:         "state.db を消したうえで Kindle にだけ在る Book があれば、Create 1 件の Initial Sync の確認を要して exit 3 で終わる",
			books:        append(slices.Clone(stateLossBooks), dryRunBookNew),
			wantCode:     3,
			wantStderr:   wantStateLossNotConfirmedErr,
			wantRequests: stateLossReadRequests,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupXDG(t, validConfigTOML)
			requests, _ := setupDryRunNotion(t, stateLossReplies())
			stubSyncPrompter(t, "", false)
			stateLossSyncOnce(t, p)
			before := len(requests())

			stubBrowser(t, newStateLossBrowser(tt.books), nil)
			code, stdout, stderr := runSyncApply(t)
			if code != tt.wantCode {
				t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
			}
			if stderr != tt.wantStderr {
				t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
			}
			if tt.wantCells != nil {
				if got := planCells(t, stdout); !reflect.DeepEqual(got, tt.wantCells) {
					t.Errorf("stdout cells = %q, want %q", got, tt.wantCells)
				}
			}
			got := requests()[before:]
			assertRequests(t, got, tt.wantRequests...)
			for _, r := range got {
				if r.method == http.MethodPatch && strings.HasPrefix(r.path, testPagesPath+"/") {
					// §12 の Subsequent Sync 列。title と Kindle ID Property は body に入らない
					for _, banned := range []string{`"title"`, dryRunKindleIDProp, dryRunBookExact.Title, stateLossBookSecond.Title} {
						if strings.Contains(r.body, banned) {
							t.Errorf("PATCH %s body = %s, want no %s", r.path, r.body, banned)
						}
					}
				}
			}
			if recorded := initialSyncRecorded(t, p); recorded != tt.wantRecorded {
				t.Errorf("initial_sync_completed_at recorded = %v, want %v", recorded, tt.wantRecorded)
			}
			if tt.wantRecorded {
				assertStateLossRebuilt(t, p)
			}
		})
	}
}

// assertStateLossRebuilt は作り直した state.db に、全 Highlight が Block ID 付きで highlights に、全 Book が books に在ることを確かめる。
func assertStateLossRebuilt(t *testing.T, p config.Paths) {
	t.Helper()
	store := state.NewSQLiteStateStore(p.StateFile())
	for _, book := range stateLossBooks {
		hs, err := store.ListHighlightIDs(context.Background(), book.ID)
		if err != nil {
			t.Fatalf("ListHighlightIDs(%s) error = %v, want nil", book.ID, err)
		}
		gotIDs := make([]string, 0, len(hs))
		for id, h := range hs {
			gotIDs = append(gotIDs, id)
			if h.DestinationHighlightID == "" {
				t.Errorf("highlights[%s].DestinationHighlightID = %q, want a Block ID", id, h.DestinationHighlightID)
			}
		}
		slices.Sort(gotIDs)
		var wantIDs []string
		for _, h := range stateLossHighlights[book.ID] {
			wantIDs = append(wantIDs, h.ID)
		}
		slices.Sort(wantIDs)
		if !slices.Equal(gotIDs, wantIDs) {
			t.Errorf("highlights of %s = %q, want %q", book.ID, gotIDs, wantIDs)
		}
	}
	books, err := store.ListBooks(context.Background())
	if err != nil {
		t.Fatalf("ListBooks() error = %v, want nil", err)
	}
	for _, book := range stateLossBooks {
		if _, ok := books[book.ID]; !ok {
			t.Errorf("books[%s] recorded = false, want true", book.ID)
		}
	}
}
