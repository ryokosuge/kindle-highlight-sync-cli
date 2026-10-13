package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

var (
	fatalBook1 = domain.Book{ID: "B000000011", ASIN: "B000000011", Title: "Distinctive Fatal Book One"}
	fatalBook2 = domain.Book{ID: "B000000012", ASIN: "B000000012", Title: "Distinctive Fatal Book Two"}
	fatalBook3 = domain.Book{ID: "B000000013", ASIN: "B000000013", Title: "Distinctive Fatal Book Three"}
)

const (
	fatalPage1Path   = testPagesPath + "/page-fatal-1"
	fatalPage2Path   = testPagesPath + "/page-fatal-2"
	fatalBlocks1Path = "/v1/blocks/page-fatal-1/children"
	fatalBlocks2Path = "/v1/blocks/page-fatal-2/children"
	// 3 冊目に要求が届いたかを見る目印（3 冊目のページの path に含まれる）
	fatalBook3Page = "page-fatal-3"
)

// markBookFailStore は本物の State を包み、failID の Book の MarkBookSynced だけを失敗させる。
type markBookFailStore struct {
	state.StateStore
	failID string
}

func (s *markBookFailStore) MarkBookSynced(ctx context.Context, b state.BookState) error {
	if b.KindleBookID == s.failID {
		return exitcode.New(exitcode.LocalState, errors.New("state.db: disk I/O error"))
	}
	return s.StateStore.MarkBookSynced(ctx, b)
}

// fatalPageJSON は Books Database の page の JSON。
func fatalPageJSON(n int, b domain.Book) string {
	return dryRunPageJSON(fmt.Sprintf("page-fatal-%d", n), b.ID, b.Title)
}

// fatalReplies は 3 冊とも Data Source にページがあるときの Notion の応答。
func fatalReplies(pages ...string) map[string]string {
	replies := map[string]string{testDryRunQueryPath: queryBody(pages...), testPagesPath: `{"object":"page","id":"page-fatal-created"}`}
	for n := 1; n <= 3; n++ {
		id := fmt.Sprintf("page-fatal-%d", n)
		replies[testPagesPath+"/"+id] = fmt.Sprintf(`{"object":"page","id":%q}`, id)
		replies["/v1/blocks/"+id+"/children"] = blockListBody()
	}
	return replies
}

// setupFatalNotion は fail に "METHOD path" で挙げた要求に status の失敗を返し、それ以外は replies を返す Notion の代役に newNotionClient を向ける。
func setupFatalNotion(t *testing.T, replies map[string]string, fail map[string]int) func() []recordedRequest {
	t.Helper()
	keyring.MockInit()
	t.Setenv(secrets.EnvNotionToken, testDryRunToken)
	reply := dryRunNotionReply(t, replies)
	var mu sync.Mutex
	var recorded []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		path := r.URL.EscapedPath()
		mu.Lock()
		recorded = append(recorded, recordedRequest{method: r.Method, path: path})
		status, failed := fail[r.Method+" "+path]
		out := ""
		if !failed {
			out = reply(r.Method, path, body)
		}
		mu.Unlock()
		if failed {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"object":"error","status":%d,"code":"failure","message":"failure"}`, status)
			return
		}
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	stubNewNotionClient(t, srv)
	return func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(recorded)
	}
}

func TestSyncFatalStop(t *testing.T) {
	// 1 冊目は追記まで済み、2 冊目で失敗し、3 冊目には何も届かない run の 1 冊目の要求
	book1Requests := []string{"POST " + testDryRunQueryPath, "PATCH " + fatalPage1Path, "GET " + fatalBlocks1Path, "PATCH " + fatalBlocks1Path}
	allPages := []string{fatalPageJSON(1, fatalBook1), fatalPageJSON(2, fatalBook2), fatalPageJSON(3, fatalBook3)}
	tests := []struct {
		name         string
		pages        []string          // Data Source のページ。2 冊目が無ければ none になって作成する
		replies      map[string]string // fatalReplies への上書き
		fail         map[string]int    // "METHOD path" ごとの失敗の status
		highlightErr map[string]error
		failMarkID   string // この Book の books の記録を失敗させる
		wantCode     int
		wantHLBooks  []domain.Book
		wantRequests []string
	}{
		{
			name:         "2 冊目の更新が Notion の 401 なら exit 4",
			pages:        allPages,
			fail:         map[string]int{"PATCH " + fatalPage2Path: http.StatusUnauthorized},
			wantCode:     4,
			wantHLBooks:  []domain.Book{fatalBook1},
			wantRequests: append(slices.Clone(book1Requests), "PATCH "+fatalPage2Path),
		},
		{
			name:         "2 冊目の作成が Data Source の 404 なら exit 4",
			pages:        []string{allPages[0], allPages[2]},
			fail:         map[string]int{"POST " + testPagesPath: http.StatusNotFound},
			wantCode:     4,
			wantHLBooks:  []domain.Book{fatalBook1},
			wantRequests: append(slices.Clone(book1Requests), "POST "+testPagesPath),
		},
		{
			name:         "2 冊目の Highlight のページが全 Book に共通の枠の異常なら exit 7",
			pages:        allPages,
			highlightErr: map[string]error{fatalBook2.ID: errHighlightsFrame},
			wantCode:     7,
			wantHLBooks:  []domain.Book{fatalBook1, fatalBook2},
			wantRequests: append(slices.Clone(book1Requests), "PATCH "+fatalPage2Path),
		},
		{
			name:         "2 冊目の Sync Marker の ID 方式が採用方式と違えば exit 6",
			pages:        allPages,
			replies:      map[string]string{fatalBlocks2Path: blockListBody(dryRunBlockJSON("block-1", "text\nkhs:h1-SECRETREST"))},
			wantCode:     6,
			wantHLBooks:  []domain.Book{fatalBook1, fatalBook2},
			wantRequests: append(slices.Clone(book1Requests), "PATCH "+fatalPage2Path, "GET "+fatalBlocks2Path),
		},
		{
			name:        "2 冊目の books の記録が失敗なら exit 6",
			pages:       allPages,
			failMarkID:  fatalBook2.ID,
			wantCode:    6,
			wantHLBooks: []domain.Book{fatalBook1, fatalBook2},
			wantRequests: append(slices.Clone(book1Requests), "PATCH "+fatalPage2Path, "GET "+fatalBlocks2Path,
				"PATCH "+fatalBlocks2Path),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupSyncedXDG(t, validConfigTOML)
			replies := fatalReplies(tt.pages...)
			for path, body := range tt.replies {
				replies[path] = body
			}
			requests := setupFatalNotion(t, replies, tt.fail)
			if tt.failMarkID != "" {
				old := newStateStore
				newStateStore = func(p config.Paths) state.StateStore {
					return &markBookFailStore{StateStore: state.NewSQLiteStateStore(p.StateFile()), failID: tt.failMarkID}
				}
				t.Cleanup(func() { newStateStore = old })
			}
			b := newDryRunBrowser()
			b.books = []domain.Book{fatalBook1, fatalBook2, fatalBook3}
			b.highlights = map[string][]domain.Highlight{}
			for _, book := range b.books {
				b.highlights[book.ID] = []domain.Highlight{{ID: "a-" + book.ID, BookID: book.ID, Text: "Distinctive Highlight"}}
			}
			b.highlightErr = tt.highlightErr
			stubBrowser(t, b, nil)

			code, _, stderr := runSyncApply(t)
			if code != tt.wantCode {
				t.Errorf("run(sync) = %d, want %d (stderr = %q)", code, tt.wantCode, stderr)
			}
			if strings.Contains(stderr, "were not synced") {
				t.Errorf("stderr = %q, want no failed Book list", stderr)
			}
			if !slices.Equal(b.hlBooks, tt.wantHLBooks) {
				t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, tt.wantHLBooks)
			}
			assertRequests(t, requests(), tt.wantRequests...)
			for _, r := range requests() {
				if strings.Contains(r.path, fatalBook3Page) {
					t.Errorf("request %s %s reached the 3rd Book, want none", r.method, r.path)
				}
			}
			if !b.closed {
				t.Error("browser not closed")
			}
		})
	}

	t.Run("state.db を開けない run は Notion に要求を出さずに exit 6 で終わる", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		// state.db の場所にディレクトリを置いて開けなくする
		if err := os.MkdirAll(p.StateFile(), 0o755); err != nil {
			t.Fatal(err)
		}
		requests := setupFatalNotion(t, fatalReplies(fatalPageJSON(1, fatalBook1)), nil)
		b := newDryRunBrowser()
		b.books = []domain.Book{fatalBook1, fatalBook2, fatalBook3}
		stubBrowser(t, b, nil)

		code, _, stderr := runSyncApply(t, "--yes")
		if code != 6 {
			t.Errorf("run(sync --yes) = %d, want 6 (stderr = %q)", code, stderr)
		}
		if strings.Contains(stderr, "were not synced") {
			t.Errorf("stderr = %q, want no failed Book list", stderr)
		}
		assertRequests(t, requests())
		if len(b.hlBooks) != 0 {
			t.Errorf("ListHighlights() books = %+v, want none", b.hlBooks)
		}
	})
}
