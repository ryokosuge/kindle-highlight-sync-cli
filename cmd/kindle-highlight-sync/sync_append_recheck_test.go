package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

// 追記の確かめ直しの end-to-end の fixture。どちらの Book も exact と照合され、ページの子 block は空から始まる。
const (
	recheckPageA = "page-recheck-a"
	recheckPageB = "page-recheck-b"
)

const (
	recheckBookAID    = "B000000011"
	recheckBookATitle = "Distinctive Recheck Book A"
	recheckBookBID    = "B000000012"
	recheckBookBTitle = "Distinctive Recheck Book B"
	recheckHighlightB = "a-RECHECK-4"
)

// newRecheckFixture は recheckBookA・recheckBookB とその Highlight を毎回新しく作る。サブテスト間で map や slice を共有しない。
func newRecheckFixture() (books []domain.Book, highlights map[string][]domain.Highlight) {
	books = []domain.Book{
		{ID: recheckBookAID, ASIN: recheckBookAID, Title: recheckBookATitle},
		{ID: recheckBookBID, ASIN: recheckBookBID, Title: recheckBookBTitle},
	}
	highlights = map[string][]domain.Highlight{
		recheckBookAID: {
			{ID: "a-RECHECK-1", BookID: recheckBookAID, Text: "Distinctive Recheck One"},
			{ID: "a-RECHECK-2", BookID: recheckBookAID, Text: "Distinctive Recheck Two"},
			{ID: "a-RECHECK-3", BookID: recheckBookAID, Text: "Distinctive Recheck Three"},
		},
		recheckBookBID: {{ID: recheckHighlightB, BookID: recheckBookBID, Text: "Distinctive Recheck Four"}},
	}
	return books, highlights
}

// recheckNotion は追記した Quote Block をページごとに覚えて GET children で返す Notion の代役。
// appendFault が非 nil なら、そのページへの n 回目（1 始まり）の追記の要求の扱いを決める。
type recheckNotion struct {
	mu          sync.Mutex
	blocks      map[string][]string // page ID ごとの Quote Block の JSON
	appendCalls map[string]int      // page ID ごとの追記の要求の回数
	appendFault func(page string, n int, children []string) (stored []string, status int)
}

// handle は method・path ごとの応答を書く。追記は fault が返した分だけを覚え、status が 200 以外ならその status を返す。
func (n *recheckNotion) handle(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		body, _ := io.ReadAll(r.Body)
		n.mu.Lock()
		defer n.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && path == testDryRunQueryPath:
			_, _ = w.Write([]byte(queryBody(
				dryRunPageJSON(recheckPageA, recheckBookAID, recheckBookATitle),
				dryRunPageJSON(recheckPageB, recheckBookBID, recheckBookBTitle),
			)))
		case r.Method == http.MethodPatch && strings.HasPrefix(path, testPagesPath+"/"):
			_, _ = fmt.Fprintf(w, `{"object":"page","id":%q}`, strings.TrimPrefix(path, testPagesPath+"/"))
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/blocks/"):
			_, _ = w.Write([]byte(blockListBody(n.blocks[recheckPageID(path)]...)))
		case r.Method == http.MethodPatch && strings.HasPrefix(path, "/v1/blocks/"):
			page := recheckPageID(path)
			n.appendCalls[page]++
			children, err := recheckChildren(body)
			if err != nil {
				// handler は server 側の goroutine で動くので t.Fatalf は呼ばず、非致命で報告して HTTP エラーを返す
				t.Errorf("decode append request error = %v, want nil", err)
				http.Error(w, "bad append request", http.StatusBadRequest)
				return
			}
			stored, status := children, http.StatusOK
			if n.appendFault != nil {
				stored, status = n.appendFault(page, n.appendCalls[page], children)
			}
			n.blocks[page] = append(n.blocks[page], stored...)
			w.WriteHeader(status)
			if status == http.StatusOK {
				_, _ = w.Write([]byte(blockListBody(stored...)))
				return
			}
			_, _ = fmt.Fprintf(w, `{"object":"error","status":%d,"code":"internal_server_error","message":"failed"}`, status)
		default:
			t.Errorf("request = %s %s, want POST %s, PATCH %s/{id}, or GET|PATCH /v1/blocks/{id}/children", r.Method, path, testDryRunQueryPath, testPagesPath)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}
}

// recheckPageID は /v1/blocks/{page_id}/children の page_id を返す。
func recheckPageID(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "/v1/blocks/"), "/children")
}

// recheckChildren は追記の要求の body の Quote Block を、GET children が返す形の JSON にして順に返す。
func recheckChildren(body []byte) ([]string, error) {
	var req struct {
		Children []struct {
			Quote struct {
				RichText []struct {
					Text struct {
						Content string `json:"content"`
					} `json:"text"`
				} `json:"rich_text"`
			} `json:"quote"`
		} `json:"children"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	var blocks []string
	for i, c := range req.Children {
		var text strings.Builder
		for _, rt := range c.Quote.RichText {
			text.WriteString(rt.Text.Content)
		}
		blocks = append(blocks, dryRunBlockJSON(fmt.Sprintf("block-%d-%s", i, text.String()), text.String()))
	}
	return blocks, nil
}

// setupRecheckNotion は n を受ける httptest の Notion を newNotionClient に向け、確かめ直しの前の待ちを 0 にする。
func setupRecheckNotion(t *testing.T, n *recheckNotion) {
	t.Helper()
	keyring.MockInit()
	t.Setenv(secrets.EnvNotionToken, testDryRunToken)
	n.blocks, n.appendCalls = map[string][]string{}, map[string]int{}
	srv := httptest.NewServer(n.handle(t))
	t.Cleanup(srv.Close)
	stubNewNotionClient(t, srv)
	orig := syncengine.RecheckSleep
	syncengine.RecheckSleep = func(_ context.Context, _ time.Duration) error { return nil }
	t.Cleanup(func() { syncengine.RecheckSleep = orig })
}

// newRecheckBrowser は newRecheckFixture の Book とその Highlight を返す fake。
func newRecheckBrowser() *fakeBrowser {
	b := newDryRunBrowser()
	b.books, b.highlights = newRecheckFixture()
	return b
}

// markerCount は blocks のうち Sync Marker khs:<highlightID> を持つものの数を返す。
func markerCount(blocks []string, highlightID string) int {
	n := 0
	for _, b := range blocks {
		if strings.Contains(b, "khs:"+highlightID) {
			n++
		}
	}
	return n
}

func TestSyncAppendRecheck(t *testing.T) {
	tests := []struct {
		name        string
		fault       func(page string, call int, children []string) ([]string, int) // 追記の要求の失敗のさせ方
		wantCode    int
		wantStderr  string
		wantCells   [][]string // 実行結果の件数（対象の数の行を除く）
		wantCallsA  int        // Book A のページへの追記の要求の回数
		wantBlocksA int        // Book A のページに残る Quote Block の数
		wantBlocksB int        // Book B のページに残る Quote Block の数
	}{
		{
			name: "1 回目の追記が一部を反映して 504 を返しても、Sync Marker は重複せず全部の Highlight がそろい、確かめ直した Book と送り直した件数を出して exit 0 で終わる",
			fault: func(page string, call int, children []string) ([]string, int) {
				if page == recheckPageA && call == 1 {
					return children[:2], http.StatusGatewayTimeout // 3 件のうち 2 件を反映してから 504
				}
				return children, http.StatusOK
			},
			wantCode:    0,
			wantCells:   recheckCells(applyCells("0", "0", "4", "0", "0"), "1", "1"),
			wantCallsA:  2,
			wantBlocksA: 3,
			wantBlocksB: 1,
		},
		{
			name: "1 回目の追記が何も反映せず 502 を返しても、確かめ直して 3 件を送り直し、重複せず exit 0 で終わる",
			fault: func(page string, call int, children []string) ([]string, int) {
				if page == recheckPageA && call == 1 {
					return nil, http.StatusBadGateway
				}
				return children, http.StatusOK
			},
			wantCode:    0,
			wantCells:   recheckCells(applyCells("0", "0", "4", "0", "0"), "1", "3"),
			wantCallsA:  2,
			wantBlocksA: 3,
			wantBlocksB: 1,
		},
		{
			name: "同じ Book への追記が毎回 500 を返すと、その Book ID を標準エラーに出して exit 8 で終わり、他の Book の追記は届く",
			fault: func(page string, _ int, children []string) ([]string, int) {
				if page == recheckPageA {
					return nil, http.StatusInternalServerError
				}
				return children, http.StatusOK
			},
			wantCode:   8,
			wantStderr: "partial failure: sync: 1 books were not synced: " + recheckBookAID + " (notion write failed)\n",
			// 1 回目 + 確かめ直しのたびの再送 2 回。送り直した Highlight は 3 件を 2 回
			wantCells:   recheckCells(applyCells("0", "0", "1", "0", "1"), "1", "6"),
			wantCallsA:  3,
			wantBlocksA: 0,
			wantBlocksB: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupXDG(t, validConfigTOML)
			n := &recheckNotion{appendFault: tt.fault}
			setupRecheckNotion(t, n)
			stubSyncPrompter(t, "", false)
			stubBrowser(t, newRecheckBrowser(), nil)

			code, stdout, stderr := runSyncApply(t, "--yes")
			if code != tt.wantCode || stderr != tt.wantStderr {
				t.Errorf("run(sync --yes) code = %d, stderr = %q, want %d and %q", code, stderr, tt.wantCode, tt.wantStderr)
			}
			if got, want := planCells(t, stdout), withTargetCount(tt.wantCells, "2", "2"); !reflect.DeepEqual(got, want) {
				t.Errorf("stdout cells = %q, want %q", got, want)
			}
			if got := n.appendCalls[recheckPageA]; got != tt.wantCallsA {
				t.Errorf("n.appendCalls[recheckPageA] = %d, want %d", got, tt.wantCallsA)
			}
			_, highlights := newRecheckFixture()
			if got := len(n.blocks[recheckPageA]); got != tt.wantBlocksA {
				t.Errorf("len(n.blocks[recheckPageA]) = %d, want %d", got, tt.wantBlocksA)
			}
			if tt.wantBlocksA > 0 {
				for _, h := range highlights[recheckBookAID] {
					if got := markerCount(n.blocks[recheckPageA], h.ID); got != 1 {
						t.Errorf("markerCount(%s) = %d, want 1", h.ID, got)
					}
				}
			}
			if got := len(n.blocks[recheckPageB]); got != tt.wantBlocksB {
				t.Errorf("len(n.blocks[recheckPageB]) = %d, want %d", got, tt.wantBlocksB)
			}
			if got, want := markerCount(n.blocks[recheckPageB], recheckHighlightB), tt.wantBlocksB; got != want {
				t.Errorf("markerCount(%s) = %d, want %d", recheckHighlightB, got, want)
			}
			// 標準出力は件数だけで、Highlight の本文・Note・token・Sync Marker は出さない
			// 失敗メッセージに出力そのものは入れない（token が漏れうる）
			for _, secret := range []struct{ name, value string }{{"Highlight の本文", "Distinctive"}, {"token", testDryRunToken}, {"Sync Marker", "khs:"}} {
				if got := strings.Contains(stdout+stderr, secret.value); got {
					t.Errorf("strings.Contains(stdout+stderr, %s) = %v, want false", secret.name, got)
				}
			}
		})
	}
}
