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
	// recoverAnnotatedAt は 1 回目の last_synced_at より後なので、2 回目も Book が対象になる。
	recoverAnnotatedAt = time.Now().AddDate(1, 0, 0).UTC().Truncate(24 * time.Hour)
)

// recoverMarkerCount は Notion のページにある khs:<id> の数。初めからある block の応答と、追記の要求の body を数える。
func recoverMarkerCount(id, blocksReply string, appendBodies []string) int {
	return strings.Count(blocksReply+strings.Join(appendBodies, ""), "khs:"+id)
}

func TestSyncRecoverInterrupt(t *testing.T) {
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

	newBrowser := func(highlights ...domain.Highlight) *fakeBrowser {
		book := dryRunBookExact
		book.LastAnnotatedAt = &recoverAnnotatedAt
		b := newDryRunBrowser()
		b.books = []domain.Book{book}
		b.highlights = map[string][]domain.Highlight{book.ID: highlights}
		return b
	}
	exactHighlights := dryRunHighlights[dryRunBookExact.ID]

	// 1 回目: 同期済みにする（a-HIGHLIGHT-1 は Notion の既知、a-NOTE-2 を追記する）
	stubBrowser(t, newBrowser(exactHighlights...), nil)
	if code, stdout, stderr := runSyncApply(t); code != 0 || stderr != "" {
		t.Fatalf("run 1: run(sync) = %d, stdout = %q, stderr = %q, want 0 and empty stderr", code, stdout, stderr)
	}

	// 追記の後・保存の前に止まった状態: Notion には Sync Marker があり、State の highlights には無い
	interruptedBlock := dryRunBlockJSON("block-interrupted", recoverInterrupted.Text+"\nkhs:"+recoverInterrupted.ID)
	replies[testDryRunBlocksPath] = blockListBody(
		dryRunBlockJSON("block-1", exactHighlights[0].Text+"\nkhs:"+exactHighlights[0].ID),
		interruptedBlock,
	)
	initialBlocksReply := replies[testDryRunBlocksPath]
	bodiesBeforeRun2 := len(appendBodies)

	// 2 回目: Kindle に Highlight が 2 つ増える
	b := newBrowser(append(slices.Clone(exactHighlights), recoverInterrupted, recoverNew)...)
	stubBrowser(t, b, nil)
	before := len(requests())
	code, stdout, stderr := runSyncApply(t)
	if code != 0 || stderr != "" {
		t.Fatalf("run 2: run(sync) = %d, stderr = %q, want 0 and empty", code, stderr)
	}
	if got := highlightBookIDs(b); !slices.Equal(got, []string{dryRunBookExact.ID}) {
		t.Errorf("run 2: highlight book IDs = %q, want %q", got, []string{dryRunBookExact.ID})
	}
	if got, want := planCells(t, stdout), withTargetCount(applyCells("0", "0", "1", "0", "0"), "1", "1"); !reflect.DeepEqual(got, want) {
		t.Errorf("run 2: stdout cells = %q, want %q", got, want)
	}
	if got, want := appendRequests(requests()[before:]), []string{"PATCH " + testDryRunBlocksPath}; !slices.Equal(got, want) {
		t.Errorf("run 2: append requests = %q, want %q", got, want)
	}
	if got := len(appendBodies) - bodiesBeforeRun2; got != 1 {
		t.Fatalf("run 2: len(append bodies) = %d, want 1", got)
	}
	appended := appendBodies[bodiesBeforeRun2]
	if strings.Contains(appended, "khs:"+recoverInterrupted.ID) {
		t.Errorf("run 2: append body = %s, want no Sync Marker of %s", appended, recoverInterrupted.ID)
	}
	if !strings.Contains(appended, "khs:"+recoverNew.ID) {
		t.Errorf("run 2: append body = %s, want Sync Marker of %s", appended, recoverNew.ID)
	}

	// 止まっていた Highlight も新しい Highlight も Block ID 付きで state.db に残る
	states, err := state.NewSQLiteStateStore(p.StateFile()).ListHighlightIDs(context.Background(), dryRunBookExact.ID)
	if err != nil {
		t.Fatalf("ListHighlightIDs error = %v, want nil", err)
	}
	saved := map[string]string{}
	for id, h := range states {
		saved[id] = h.DestinationHighlightID
	}
	wantSaved := map[string]string{
		"a-HIGHLIGHT-1":       "block-1",
		"a-NOTE-2":            "appended-1",
		recoverInterrupted.ID: "block-interrupted",
		recoverNew.ID:         "appended-2",
	}
	if !reflect.DeepEqual(saved, wantSaved) {
		t.Errorf("saved Block IDs = %v, want %v", saved, wantSaved)
	}

	// 3 回目: 追記の要求は 0 回で、どの Highlight ID の Sync Marker もページに 1 つだけ
	stubBrowser(t, newBrowser(append(slices.Clone(exactHighlights), recoverInterrupted, recoverNew)...), nil)
	before = len(requests())
	if code, _, stderr := runSyncApply(t); code != 0 || stderr != "" {
		t.Fatalf("run 3: run(sync) = %d, stderr = %q, want 0 and empty", code, stderr)
	}
	if got := appendRequests(requests()[before:]); len(got) != 0 {
		t.Errorf("run 3: append requests = %q, want none", got)
	}
	for _, h := range append(slices.Clone(exactHighlights), recoverInterrupted, recoverNew) {
		if got := recoverMarkerCount(h.ID, initialBlocksReply, appendBodies); got != 1 {
			t.Errorf("Sync Marker count of %s = %d, want 1", h.ID, got)
		}
	}
}
