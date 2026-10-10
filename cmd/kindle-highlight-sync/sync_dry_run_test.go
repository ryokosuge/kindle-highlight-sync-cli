package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
)

// validConfigTOML の [notion] に合わせた値。dry-run の end-to-end は config.toml を読んで Notion の代役に向かう。
const (
	testDryRunToken     = "test-token-dry-run-80"
	testDryRunQueryPath = "/v1/data_sources/ds/query"
	dryRunKindleIDProp  = "Cd34"
	dryRunTitleProp     = "title"
)

var (
	dryRunBookExact     = domain.Book{ID: "B000000001", ASIN: "B000000001", Title: "Distinctive Exact Book"}
	dryRunBookNew       = domain.Book{ID: "B000000002", ASIN: "B000000002", Title: "Distinctive New Book"}
	dryRunBookAmbiguous = domain.Book{ID: "B000000003", ASIN: "B000000003", Title: "Distinctive Ambiguous Book"}
	dryRunBooks         = []domain.Book{dryRunBookExact, dryRunBookNew, dryRunBookAmbiguous}
	dryRunHighlights    = map[string][]domain.Highlight{
		dryRunBookExact.ID: {
			{ID: "a-HIGHLIGHT-1", BookID: dryRunBookExact.ID, Text: "Distinctive Highlight Text"},
			{ID: "a-NOTE-2", BookID: dryRunBookExact.ID, Note: "Distinctive Note Only"},
		},
		dryRunBookNew.ID:       {{ID: "a-HIGHLIGHT-3", BookID: dryRunBookNew.ID, Text: "Distinctive Highlight Three"}},
		dryRunBookAmbiguous.ID: {{ID: "a-HIGHLIGHT-4", BookID: dryRunBookAmbiguous.ID, Text: "Distinctive Highlight Four"}},
	}
)

// dryRunPageJSON は validConfigTOML の mapping で読める Books Database の page の JSON。kindleID が空なら Adopt の候補。
func dryRunPageJSON(id, kindleID, title string) string {
	return fmt.Sprintf(`{"object":"page","id":%q,"properties":{%s,%s}}`, id,
		pagePropertyJSON("Name", dryRunTitleProp, "title", title),
		pagePropertyJSON("Kindle ID", dryRunKindleIDProp, "rich_text", kindleID))
}

// dryRunQueryBody は exact 1 冊・ambiguous 1 冊（同じ Title のページが 2 つ）になる Data Source の応答。
func dryRunQueryBody() string {
	return queryBody(
		dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
		dryRunPageJSON("page-dup-1", "", dryRunBookAmbiguous.Title),
		dryRunPageJSON("page-dup-2", "", dryRunBookAmbiguous.Title),
	)
}

// newDryRunBrowser は Notebook に到達し dryRunBooks とその Highlight を返す fake。
func newDryRunBrowser() *fakeBrowser {
	return &fakeBrowser{
		reached:    true,
		books:      dryRunBooks,
		highlights: dryRunHighlights,
		pageURL:    "https://read.amazon.co.jp/notebook",
		pageHTML:   []byte(`<div id="kp-notebook-library"></div>`),
	}
}

// setupDryRunNotion は token を環境変数に置き、newNotionClient を body を返す Notion の代役に向ける。
func setupDryRunNotion(t *testing.T, body string) (requests func() []recordedRequest, clientCalls func() int) {
	t.Helper()
	keyring.MockInit()
	t.Setenv(secrets.EnvNotionToken, testDryRunToken)
	srv, requests := newNotionServer(t, body)
	return requests, stubNewNotionClient(t, srv)
}

// runSyncDryRun は sync --dry-run を走らせる。dry-run は標準出力に予定を出すので runSync とは別に stdout も返す。
func runSyncDryRun(t *testing.T) (code int, stdout, stderr string) {
	t.Helper()
	var stdoutBuf, stderrBuf bytes.Buffer
	code = run([]string{"sync", "--dry-run"}, &stdoutBuf, &stderrBuf)
	return code, stdoutBuf.String(), stderrBuf.String()
}

// dataDirEntries は data ディレクトリ直下のファイル名を返す。ディレクトリが無ければ nil。
func dataDirEntries(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertOnlyQueryRequests(t *testing.T, got []recordedRequest, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("requests = %+v, want %d requests", got, want)
	}
	for i, r := range got {
		if r.method != "POST" || r.path != testDryRunQueryPath {
			t.Errorf("requests[%d] = %s %s, want POST %s", i, r.method, r.path, testDryRunQueryPath)
		}
	}
}

func TestSyncDryRun(t *testing.T) {
	t.Run("排他の内側で Book を取得・照合し、予定の表と summary を標準出力に出して exit 0 で終わる", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, dryRunQueryBody())
		b := newDryRunBrowser()
		stubBrowser(t, b, nil)
		if got := dataDirEntries(t, p.DataDir); got != nil {
			t.Fatalf("data dir entries before sync = %v, want none", got)
		}

		var code int
		var stdout, stderr string
		osStdout, osStderr := captureProcessOutput(t, func() { code, stdout, stderr = runSyncDryRun(t) })
		if osStdout != "" || osStderr != "" {
			t.Errorf("process stdout = %q, stderr = %q, want both empty", osStdout, osStderr)
		}
		assertSyncSucceeded(t, code, stderr, filepath.Join(p.DataDir, "run.lock"))
		want := [][]string{
			{"Kindle Book", "Match Status", "Matched Notion Page", "Action"},
			{dryRunBookExact.Title, "exact", "page-exact", "append"},
			{dryRunBookNew.Title, "none", "-", "create + append"},
			{dryRunBookAmbiguous.Title, "ambiguous", "-", "skip"},
			{},
			{"Exact matches", "1"},
			{"Adoptable matches", "0"},
			{"New books", "1"},
			{"Ambiguous", "1"},
			{"Highlights to append", "3"},
		}
		if got := planCells(t, stdout); !reflect.DeepEqual(got, want) {
			t.Errorf("run(sync --dry-run) stdout cells = %q, want %q", got, want)
		}
		// ambiguous の Book は Highlight を取りに行かず、ほかの Book も 1 回ずつしか取らない
		if want := []domain.Book{dryRunBookExact, dryRunBookNew}; !slices.Equal(b.hlBooks, want) {
			t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, want)
		}
		if !b.closed {
			t.Error("browser not closed")
		}
		// Notion への要求は Data Source の query だけで、ページ・Property・block を変える要求は無い
		assertOnlyQueryRequests(t, requests(), 1)
		// state.db は作られず、data ディレクトリには run.lock しか増えない
		if got, want := dataDirEntries(t, p.DataDir), []string{"run.lock"}; !slices.Equal(got, want) {
			t.Errorf("data dir entries after sync = %v, want %v", got, want)
		}
	})

	t.Run("認証切れ・同期先の組み立ての失敗・Notion の取得の失敗では予定を出さずに止まる", func(t *testing.T) {
		keychainErr := errors.New("keychain boom")
		tests := []struct {
			name         string
			configTOML   string
			token        string
			keychainErr  error // nil 以外なら Keychain の読み取りがこの失敗を返す
			notionBody   string
			pageHTML     []byte
			wantCode     int
			wantStderr   func(p config.Paths) string
			wantRequests int
			wantHLBooks  int
		}{
			{
				name:       "認証切れでは exit 2",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				notionBody: dryRunQueryBody(),
				pageHTML:   []byte(`<form name="signIn"><input name="email"></form>`),
				wantCode:   2,
				wantStderr: func(config.Paths) string {
					return "Amazon authentication required: sync: a login form is present at \"read.amazon.co.jp/notebook\" without the Kindle Notebook library; run `kindle-highlight-sync login` to sign in again\n"
				},
			},
			{
				name:       "Notion の取得の失敗では exit 4",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				notionBody: "not json",
				wantCode:   4,
				wantStderr: func(config.Paths) string {
					return "Notion error: notion: POST " + testDryRunQueryPath + ": invalid response JSON\n"
				},
				wantRequests: 1,
			},
			{
				name:       "config.toml が無ければ exit 3",
				configTOML: "",
				token:      testDryRunToken,
				notionBody: dryRunQueryBody(),
				wantCode:   3,
				wantStderr: func(p config.Paths) string {
					return "configuration / confirmation required: " + p.ConfigFile + " not found: run `kindle-highlight-sync init` to create it\n"
				},
			},
			{
				name:       "token が無ければ exit 3",
				configTOML: validConfigTOML,
				token:      "",
				notionBody: dryRunQueryBody(),
				wantCode:   3,
				wantStderr: func(config.Paths) string {
					return "configuration / confirmation required: notion token not found in KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN or Keychain: run `kindle-highlight-sync init` to save it\n"
				},
			},
			{
				// newDestination（#79）は ErrNotFound 以外の Keychain の失敗に種類を付けないので、report は General（exit 1）にする
				name:        "Keychain の読み取りの失敗は種類を持たない exit 1",
				configTOML:  validConfigTOML,
				token:       "",
				keychainErr: keychainErr,
				notionBody:  dryRunQueryBody(),
				wantCode:    1,
				wantStderr: func(config.Paths) string {
					return "general error: cannot read Notion token in Keychain (service kindle-highlight-sync, account notion-token)\n"
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, tt.configTOML)
				requests, _ := setupDryRunNotion(t, tt.notionBody)
				t.Setenv(secrets.EnvNotionToken, tt.token)
				if tt.keychainErr != nil {
					keyring.MockInitWithError(tt.keychainErr)
				}
				b := newDryRunBrowser()
				if tt.pageHTML != nil {
					b.pageHTML = tt.pageHTML
				}
				stubBrowser(t, b, nil)

				code, stdout, stderr := runSyncDryRun(t)
				if code != tt.wantCode {
					t.Errorf("run(sync --dry-run) code = %d, want %d", code, tt.wantCode)
				}
				if stdout != "" {
					t.Errorf("run(sync --dry-run) stdout = %q, want empty", stdout)
				}
				if want := tt.wantStderr(p); stderr != want {
					t.Errorf("run(sync --dry-run) stderr = %q, want %q", stderr, want)
				}
				if len(b.hlBooks) != tt.wantHLBooks {
					t.Errorf("ListHighlights() books = %+v, want %d Books", b.hlBooks, tt.wantHLBooks)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
				assertOnlyQueryRequests(t, requests(), tt.wantRequests)
			})
		}
	})

	t.Run("--dry-run を付けない sync は Notion に要求せず、標準出力に何も出さない", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		requests, clientCalls := setupDryRunNotion(t, dryRunQueryBody())
		b := newDryRunBrowser()
		stubBrowser(t, b, nil)

		code, stderr := runSync(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if !slices.Equal(b.hlBooks, dryRunBooks) {
			t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, dryRunBooks)
		}
		if got := clientCalls(); got != 0 {
			t.Errorf("newNotionClient calls = %d, want 0", got)
		}
		assertOnlyQueryRequests(t, requests(), 0)
	})

	t.Run("本物の sync が排他を持っている間の dry-run は待たずに exit 9 で終わる", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		_, clientCalls := setupDryRunNotion(t, dryRunQueryBody())
		_, s := stubReachedBrowser(t)
		release, done := holdLock(t, "sync")

		code, stdout, stderr := runSyncDryRun(t)
		if code != 9 {
			t.Errorf("run(sync --dry-run) code = %d, want 9", code)
		}
		if stdout != "" {
			t.Errorf("run(sync --dry-run) stdout = %q, want empty", stdout)
		}
		if stderr != wantAlreadyRunningErr {
			t.Errorf("run(sync --dry-run) stderr = %q, want %q", stderr, wantAlreadyRunningErr)
		}
		if s.opened != 0 || clientCalls() != 0 {
			t.Errorf("openBrowser called %d times and newNotionClient %d times while sync holds the lock, want 0 and 0", s.opened, clientCalls())
		}

		close(release)
		if got := <-done; got.code != 0 || got.stderr != "" {
			t.Errorf("first run(sync) code = %d, stderr = %q, want 0 and empty", got.code, got.stderr)
		}
	})
}
