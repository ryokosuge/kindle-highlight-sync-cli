package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
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

var (
	recheckBookA = domain.Book{ID: "B000000011", ASIN: "B000000011", Title: "Distinctive Recheck Book A"}
	recheckBookB = domain.Book{ID: "B000000012", ASIN: "B000000012", Title: "Distinctive Recheck Book B"}
	recheckHL    = map[string][]domain.Highlight{
		recheckBookA.ID: {
			{ID: "a-RECHECK-1", BookID: recheckBookA.ID, Text: "Distinctive Recheck One"},
			{ID: "a-RECHECK-2", BookID: recheckBookA.ID, Text: "Distinctive Recheck Two"},
			{ID: "a-RECHECK-3", BookID: recheckBookA.ID, Text: "Distinctive Recheck Three"},
		},
		recheckBookB.ID: {{ID: "a-RECHECK-4", BookID: recheckBookB.ID, Text: "Distinctive Recheck Four"}},
	}
)

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
				dryRunPageJSON(recheckPageA, recheckBookA.ID, recheckBookA.Title),
				dryRunPageJSON(recheckPageB, recheckBookB.ID, recheckBookB.Title),
			)))
		case r.Method == http.MethodPatch && strings.HasPrefix(path, testPagesPath+"/"):
			_, _ = fmt.Fprintf(w, `{"object":"page","id":%q}`, strings.TrimPrefix(path, testPagesPath+"/"))
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/blocks/"):
			_, _ = w.Write([]byte(blockListBody(n.blocks[recheckPageID(path)]...)))
		case r.Method == http.MethodPatch && strings.HasPrefix(path, "/v1/blocks/"):
			page := recheckPageID(path)
			n.appendCalls[page]++
			children := recheckChildren(t, body)
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
			t.Errorf("unexpected request %s %s", r.Method, path)
			_, _ = w.Write([]byte("{}"))
		}
	}
}

// recheckPageID は /v1/blocks/{page_id}/children の page_id を返す。
func recheckPageID(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "/v1/blocks/"), "/children")
}

// recheckChildren は追記の要求の body の Quote Block を、GET children が返す形の JSON にして順に返す。
func recheckChildren(t *testing.T, body []byte) []string {
	t.Helper()
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
		t.Fatalf("decode append request: %v", err)
	}
	var blocks []string
	for i, c := range req.Children {
		var text strings.Builder
		for _, rt := range c.Quote.RichText {
			text.WriteString(rt.Text.Content)
		}
		blocks = append(blocks, dryRunBlockJSON(fmt.Sprintf("block-%d-%s", i, text.String()), text.String()))
	}
	return blocks
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

// newRecheckBrowser は recheckBookA・recheckBookB とその Highlight を返す fake。
func newRecheckBrowser() *fakeBrowser {
	b := newDryRunBrowser()
	b.books = []domain.Book{recheckBookA, recheckBookB}
	b.highlights = recheckHL
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
	t.Run("1 回目の追記が一部を反映して 504 を返しても、Sync Marker は重複せず全部の Highlight がそろい、確かめ直した Book と送り直した件数を出して exit 0 で終わる", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		n := &recheckNotion{appendFault: func(page string, call int, children []string) ([]string, int) {
			if page == recheckPageA && call == 1 {
				return children[:2], http.StatusGatewayTimeout // 3 件のうち 2 件を反映してから 504
			}
			return children, http.StatusOK
		}}
		setupRecheckNotion(t, n)
		stubSyncPrompter(t, "", false)
		stubBrowser(t, newRecheckBrowser(), nil)

		code, stdout, stderr := runSyncApply(t, "--yes")
		if code != 0 || stderr != "" {
			t.Errorf("run(sync --yes) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		for _, h := range recheckHL[recheckBookA.ID] {
			if got := markerCount(n.blocks[recheckPageA], h.ID); got != 1 {
				t.Errorf("markerCount(%s) = %d, want 1", h.ID, got)
			}
		}
		if got, want := len(n.blocks[recheckPageA]), 3; got != want {
			t.Errorf("len(blocks of A) = %d, want %d", got, want)
		}
		if got, want := n.appendCalls[recheckPageA], 2; got != want {
			t.Errorf("append calls of A = %d, want %d", got, want)
		}
		want := withTargetCount(recheckCells(applyCells("0", "0", "4", "0", "0"), "1", "1"), "2", "2")
		if got := planCells(t, stdout); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
	})

	t.Run("同じ Book への追記が毎回 500 を返すと、その Book ID を標準エラーに出して exit 8 で終わり、他の Book の追記は届く", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		n := &recheckNotion{appendFault: func(page string, _ int, children []string) ([]string, int) {
			if page == recheckPageA {
				return nil, http.StatusInternalServerError
			}
			return children, http.StatusOK
		}}
		setupRecheckNotion(t, n)
		stubSyncPrompter(t, "", false)
		stubBrowser(t, newRecheckBrowser(), nil)

		code, stdout, stderr := runSyncApply(t, "--yes")
		if want := "partial failure: sync: 1 books were not synced: " + recheckBookA.ID + " (notion write failed)\n"; code != 8 || stderr != want {
			t.Errorf("run(sync --yes) code = %d, stderr = %q, want 8 and %q", code, stderr, want)
		}
		if got, want := markerCount(n.blocks[recheckPageB], "a-RECHECK-4"), 1; got != want {
			t.Errorf("markerCount of B = %d, want %d", got, want)
		}
		// 1 回目 + 確かめ直しのたびの再送 2 回。送り直した Highlight は 3 件を 2 回
		if got, want := n.appendCalls[recheckPageA], 3; got != want {
			t.Errorf("append calls of A = %d, want %d", got, want)
		}
		want := withTargetCount(recheckCells(applyCells("0", "0", "1", "0", "1"), "1", "6"), "2", "2")
		if got := planCells(t, stdout); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
	})

	t.Run("標準出力には件数だけを出し、Highlight の本文・Note・token は出さない", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		n := &recheckNotion{appendFault: func(page string, call int, children []string) ([]string, int) {
			if page == recheckPageA && call == 1 {
				return nil, http.StatusBadGateway
			}
			return children, http.StatusOK
		}}
		setupRecheckNotion(t, n)
		stubSyncPrompter(t, "", false)
		stubBrowser(t, newRecheckBrowser(), nil)

		code, stdout, stderr := runSyncApply(t, "--yes")
		if code != 0 {
			t.Errorf("run(sync --yes) code = %d, stderr = %q, want 0", code, stderr)
		}
		for _, secret := range []string{"Distinctive", testDryRunToken, "khs:"} {
			if strings.Contains(stdout+stderr, secret) {
				t.Errorf("output contains %q: stdout = %q, stderr = %q", secret, stdout, stderr)
			}
		}
		if !slices.ContainsFunc(planCells(t, stdout), func(row []string) bool { return slices.Equal(row, []string{"Resent highlights", "3"}) }) {
			t.Errorf("stdout cells = %q, want to contain Resent highlights 3", planCells(t, stdout))
		}
	})
}
