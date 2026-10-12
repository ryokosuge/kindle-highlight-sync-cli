package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// savedBooks は state.db の books を Book ID の昇順で返す。
func savedBooks(t *testing.T, p config.Paths) []state.BookState {
	t.Helper()
	books, err := state.NewSQLiteStateStore(p.StateFile()).ListBooks(context.Background())
	if err != nil {
		t.Fatalf("ListBooks() error = %v, want nil", err)
	}
	var got []state.BookState
	for _, id := range dedupBookIDs {
		if b, ok := books[id]; ok {
			got = append(got, b)
		}
	}
	return got
}

// savedBookIDs は books の Book ID を dedupBookIDs の順で返す。
func savedBookIDs(books []state.BookState) []string {
	var ids []string
	for _, b := range books {
		ids = append(ids, b.KindleBookID)
	}
	return ids
}

// §28 §72: 追記を終えた Book だけが books に last_synced_at 付きで残る。
func TestSyncBookCompletion(t *testing.T) {
	// completionRun は 1 回の sync の実行と、その結果の期待値。
	type completionRun struct {
		failCreated bool // true なら page-created への追記の要求だけ 500 を返す
		wantCode    int
		wantStderr  string
		wantAppends []string
		wantBookIDs []string // run のあとの books の Book ID
	}
	allBooks := dedupBookIDs
	okBooks := []string{dryRunBookExact.ID, initialBookAdopt.ID}
	failStderr := "partial failure: sync: 1 books were not synced: " + dryRunBookNew.ID + " (append failed)\n"
	allAppends := []string{"PATCH " + testDryRunBlocksPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testInitialAdoptBlocksPath}
	tests := []struct {
		name string
		runs []completionRun
	}{
		{
			name: "追記をすべて終えた Book は last_synced_at 付きで books に残る",
			runs: []completionRun{{wantAppends: allAppends, wantBookIDs: allBooks}},
		},
		{
			name: "1 冊の追記が失敗すると、その Book は books に残らず他の Book は残る",
			runs: []completionRun{{failCreated: true, wantCode: 8, wantStderr: failStderr, wantAppends: allAppends, wantBookIDs: okBooks}},
		},
		{
			name: "失敗を止めて続けて sync すると、残りの Highlight だけを追記して books に残る",
			runs: []completionRun{
				{failCreated: true, wantCode: 8, wantStderr: failStderr, wantAppends: allAppends, wantBookIDs: okBooks},
				{wantAppends: []string{"PATCH " + testCreatedBlocksPath}, wantBookIDs: allBooks},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupSyncedXDG(t, validConfigTOML)
			keyring.MockInit()
			t.Setenv(secrets.EnvNotionToken, testDryRunToken)
			reply := dryRunNotionReply(t, dedupReplies())
			var failCreated atomic.Bool
			var mu sync.Mutex
			var requests []recordedRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.EscapedPath()
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, recordedRequest{method: r.Method, path: path})
				if failCreated.Load() && r.Method == http.MethodPatch && path == testCreatedBlocksPath {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"object":"error","status":500,"code":"internal_server_error","message":"failed"}`))
					return
				}
				_, _ = w.Write([]byte(reply(r.Method, path, body)))
			}))
			t.Cleanup(srv.Close)
			stubNewNotionClient(t, srv)

			for i, r := range tt.runs {
				failCreated.Store(r.failCreated)
				stubBrowser(t, newInitialBrowser(), nil)
				mu.Lock()
				before := len(requests)
				mu.Unlock()

				code, _, stderr := runSyncApply(t)
				if code != r.wantCode {
					t.Errorf("run %d: run(sync) = %d, want %d", i+1, code, r.wantCode)
				}
				if stderr != r.wantStderr {
					t.Errorf("run %d: stderr = %q, want %q", i+1, stderr, r.wantStderr)
				}
				mu.Lock()
				got := appendRequests(slices.Clone(requests[before:]))
				mu.Unlock()
				if !slices.Equal(got, r.wantAppends) {
					t.Errorf("run %d: append requests = %q, want %q", i+1, got, r.wantAppends)
				}
				books := savedBooks(t, p)
				if got := savedBookIDs(books); !reflect.DeepEqual(got, r.wantBookIDs) {
					t.Errorf("run %d: saved Book IDs = %q, want %q", i+1, got, r.wantBookIDs)
				}
				for _, b := range books {
					if b.LastSyncedAt.IsZero() {
						t.Errorf("run %d: books[%s].LastSyncedAt.IsZero() = true, want false", i+1, b.KindleBookID)
					}
				}
			}
		})
	}
}
