package main

import (
	"context"
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
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

const (
	createRecheckBookID    = "B000000021"
	createRecheckBookTitle = "Distinctive Create Recheck Book"
	createRecheckTimeout   = 50 * time.Millisecond
)

// createFault は n 回目（1 始まり）のページの作成の要求の扱い。
type createFault struct {
	create bool // 要求を受けてページを Data Source に足す
	hang   bool // 応答を返さず、クライアントがタイムアウトで切るまで待つ
	dup    bool // create のとき、同じ Title で Kindle ID の無いページを 2 つ足す（照合し直しても決められない）
}

// createNotion はページの作成の要求を記録し、fault に従ってページを足してから応答を返さない Notion の代役。
type createNotion struct {
	mu          sync.Mutex
	pages       []string // Data Source の応答に載せるページの JSON
	createCalls int
	pageIDs     []string
	blocks      map[string][]string
	fault       func(n int) createFault
}

func (c *createNotion) handle(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		switch {
		case r.Method == http.MethodPost && path == testDryRunQueryPath:
			resp := queryBody(c.pages...)
			c.mu.Unlock()
			_, _ = w.Write([]byte(resp))
		case r.Method == http.MethodPost && path == testPagesPath:
			c.createCalls++
			f := c.fault(c.createCalls)
			id := fmt.Sprintf("page-created-%d", c.createCalls)
			if f.create {
				if f.dup {
					c.pages = append(c.pages, dryRunPageJSON(id+"-a", "", createRecheckBookTitle), dryRunPageJSON(id+"-b", "", createRecheckBookTitle))
				} else {
					c.pages = append(c.pages, dryRunPageJSON(id, createRecheckBookID, createRecheckBookTitle))
				}
				c.pageIDs = append(c.pageIDs, id)
			}
			c.mu.Unlock()
			if f.hang {
				// 要求の context が切れる（クライアントのタイムアウト）まで応答を返さない
				<-r.Context().Done()
				return
			}
			_, _ = fmt.Fprintf(w, `{"object":"page","id":%q}`, id)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/blocks/"):
			resp := blockListBody(c.blocks[recheckPageID(path)]...)
			c.mu.Unlock()
			_, _ = w.Write([]byte(resp))
		case r.Method == http.MethodPatch && strings.HasPrefix(path, "/v1/blocks/"):
			children, err := recheckChildren(body)
			if err != nil {
				c.mu.Unlock()
				// handler は server 側の goroutine で動くので t.Fatalf は呼ばず、非致命で報告して HTTP エラーを返す
				t.Errorf("decode append request error = %v, want nil", err)
				http.Error(w, "bad append request", http.StatusBadRequest)
				return
			}
			page := recheckPageID(path)
			c.blocks[page] = append(c.blocks[page], children...)
			c.mu.Unlock()
			_, _ = w.Write([]byte(blockListBody(children...)))
		default:
			c.mu.Unlock()
			t.Errorf("request = %s %s, want POST %s, POST %s, or GET|PATCH /v1/blocks/{id}/children", r.Method, path, testDryRunQueryPath, testPagesPath)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}
}

func TestSyncCreateRecheck(t *testing.T) {
	tests := []struct {
		name            string
		fault           func(n int) createFault
		wantCode        int
		wantStderr      string
		wantCells       [][]string // 実行結果の件数（対象の数の行を除く）
		wantCreateCalls int
		wantPages       int // 追記が届いたページの数
	}{
		{
			name:            "作成の要求を受けてページを作ったうえでタイムアウトしても、ページは 1 つのまま Highlight が届き、照合し直した件数と作成済みとして使った件数を出して exit 0 で終わる",
			fault:           func(int) createFault { return createFault{create: true, hang: true} },
			wantCode:        0,
			wantCells:       createCells("0", "2", "1", "1", "0"),
			wantCreateCalls: 1,
			wantPages:       1,
		},
		{
			name: "作成の要求を受けずにタイムアウトした Book は、もう一度作成されてページが 1 つになり、作成済みとして使った件数は 0 になる",
			fault: func(n int) createFault {
				if n == 1 {
					return createFault{hang: true}
				}
				return createFault{create: true}
			},
			wantCode:        0,
			wantCells:       createCells("1", "2", "1", "0", "0"),
			wantCreateCalls: 2,
			wantPages:       1,
		},
		{
			name:            "照合し直しても決められなかった Book は、その Book ID を標準エラーに出して exit 8 で終わり、追記しない",
			fault:           func(int) createFault { return createFault{create: true, hang: true, dup: true} },
			wantCode:        8,
			wantStderr:      "partial failure: sync: 1 books were not synced: " + createRecheckBookID + " (notion write failed)\n",
			wantCells:       createCells("0", "0", "1", "0", "1"),
			wantCreateCalls: 1,
			wantPages:       0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupXDG(t, validConfigTOML)
			c := &createNotion{fault: tt.fault, blocks: map[string][]string{}}
			keyring.MockInit()
			t.Setenv(secrets.EnvNotionToken, testDryRunToken)
			srv := httptest.NewServer(c.handle(t))
			t.Cleanup(srv.Close)
			orig := newNotionClient
			newNotionClient = func(token string) *notion.Client {
				return notion.New(token, notion.WithBaseURL(srv.URL), notion.WithHTTPTimeout(createRecheckTimeout))
			}
			t.Cleanup(func() { newNotionClient = orig })
			origSleep := syncengine.RecheckSleep
			syncengine.RecheckSleep = func(_ context.Context, _ time.Duration) error { return nil }
			t.Cleanup(func() { syncengine.RecheckSleep = origSleep })
			stubSyncPrompter(t, "", false)
			b := newDryRunBrowser()
			b.books = []domain.Book{{ID: createRecheckBookID, ASIN: createRecheckBookID, Title: createRecheckBookTitle}}
			b.highlights = map[string][]domain.Highlight{createRecheckBookID: {
				{ID: "a-CREATE-1", BookID: createRecheckBookID, Text: "Distinctive Create One"},
				{ID: "a-CREATE-2", BookID: createRecheckBookID, Text: "Distinctive Create Two"},
			}}
			stubBrowser(t, b, nil)

			code, stdout, stderr := runSyncApply(t, "--yes")
			if code != tt.wantCode || stderr != tt.wantStderr {
				t.Errorf("run(sync --yes) code = %d, stderr = %q, want %d and %q", code, stderr, tt.wantCode, tt.wantStderr)
			}
			if got, want := planCells(t, stdout), withTargetCount(tt.wantCells, "1", "1"); !reflect.DeepEqual(got, want) {
				t.Errorf("stdout cells = %q, want %q", got, want)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.createCalls != tt.wantCreateCalls {
				t.Errorf("createCalls = %d, want %d", c.createCalls, tt.wantCreateCalls)
			}
			if got := len(c.blocks); got != tt.wantPages {
				t.Errorf("len(blocks) = %d, want %d", got, tt.wantPages)
			}
			if tt.wantPages > 0 {
				// Data Source の同じ Book ID のページは 1 つで、追記はそのページに届く
				if got := strings.Count(strings.Join(c.pages, ""), `"`+createRecheckBookID+`"`); got != 1 {
					t.Errorf("pages with Book ID = %d, want 1", got)
				}
				last := c.pageIDs[len(c.pageIDs)-1]
				for _, id := range []string{"a-CREATE-1", "a-CREATE-2"} {
					if got := markerCount(c.blocks[last], id); got != 1 {
						t.Errorf("markerCount(%s) = %d, want 1", id, got)
					}
				}
			}
			// 標準出力は件数だけで、Book のタイトルと token は出さない
			for _, secret := range []struct{ name, value string }{{"Book のタイトル", createRecheckBookTitle}, {"token", testDryRunToken}} {
				if got := strings.Contains(stdout+stderr, secret.value); got {
					t.Errorf("strings.Contains(stdout+stderr, %s) = %v, want false", secret.name, got)
				}
			}
		})
	}
}

// createCells は writeApplyResult が出す、照合し直した件数の行を含む件数の行。
func createCells(created, appended, refound, recovered, failed string) [][]string {
	return [][]string{{"Created", created}, {"Adopted", "0"}, {"Appended highlights", appended}, {"Refound books", refound}, {"Recovered pages", recovered}, {"Skipped (ambiguous)", "0"}, {"Failed", failed}}
}
