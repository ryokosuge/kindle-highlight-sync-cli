package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
)

// validConfigTOML の [notion] に合わせた値。dry-run の end-to-end は config.toml を読んで Notion の代役に向かう。
const (
	testDryRunToken      = "test-token-dry-run-80"
	testDryRunQueryPath  = "/v1/data_sources/ds/query"
	testDryRunBlocksPath = "/v1/blocks/page-exact/children"
	dryRunKindleIDProp   = "Cd34"
	dryRunTitleProp      = "title"
)

var (
	dryRunBookExact     = domain.Book{ID: "B000000001", ASIN: "B000000001", Title: "Distinctive Exact Book"}
	dryRunBookNew       = domain.Book{ID: "B000000002", ASIN: "B000000002", Title: "Distinctive New Book"}
	dryRunBookAmbiguous = domain.Book{ID: "B000000003", ASIN: "B000000003", Title: "Distinctive Ambiguous Book"}
	dryRunBooks         = []domain.Book{dryRunBookExact, dryRunBookNew, dryRunBookAmbiguous}
	// ASIN が無く Highlight のページを開けない Book（Book ID は §13 の b1-）
	dryRunBookNoASIN = domain.Book{ID: "b1-0123456789abcdef0123456789abcdef", Title: "Distinctive No ASIN Book"}
	dryRunHighlights = map[string][]domain.Highlight{
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

// blockListBody は Book のページの子 block の応答（最後のページ）。
func blockListBody(blocks ...string) string {
	return fmt.Sprintf(`{"object":"list","results":[%s],"has_more":false,"next_cursor":null}`, strings.Join(blocks, ","))
}

// dryRunBlockJSON は Sync Marker 付きの Quote Block の JSON。
func dryRunBlockJSON(id, text string) string {
	return fmt.Sprintf(`{"object":"block","id":%q,"type":"quote","quote":{"rich_text":[{"plain_text":%q}]}}`, id, text)
}

// dryRunReplies は path ごとの Notion の応答。exact のページの子 block には dryRunBookExact の 1 件目の Sync Marker がある。
func dryRunReplies() map[string]string {
	return map[string]string{
		testDryRunQueryPath:  dryRunQueryBody(),
		testDryRunBlocksPath: blockListBody(dryRunBlockJSON("block-1", "Distinctive Highlight Text\nkhs:"+dryRunHighlights[dryRunBookExact.ID][0].ID)),
	}
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

// setupDryRunNotion は token を環境変数に置き、newNotionClient を path ごとに replies の body を返す Notion の代役に向ける。
// replies に無い path には空の JSON を返し、Client が invalid response で止まる。
func setupDryRunNotion(t *testing.T, replies map[string]string) (requests func() []recordedRequest, clientCalls func() int) {
	t.Helper()
	keyring.MockInit()
	t.Setenv(secrets.EnvNotionToken, testDryRunToken)
	srv, requests := newNotionServerFunc(t, func(path string) string {
		if body, ok := replies[path]; ok {
			return body
		}
		return "{}"
	})
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

// assertRequests は Notion に届いた要求の method と path の列が want（"METHOD path"）と一致することを確かめる。
func assertRequests(t *testing.T, got []recordedRequest, want ...string) {
	t.Helper()
	lines := make([]string, 0, len(got))
	for _, r := range got {
		lines = append(lines, r.method+" "+r.path)
	}
	if !slices.Equal(lines, want) {
		t.Errorf("requests = %q, want %q", lines, want)
	}
}

func TestSyncDryRun(t *testing.T) {
	t.Run("排他の内側で Book を取得・照合し、予定の表と summary を標準出力に出して exit 0 で終わる", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, dryRunReplies())
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
			{"Kindle Book", "Match Status", "Matched Notion Page", "Action", "Highlights to append"},
			{dryRunBookExact.Title, "exact", "page-exact", "append", "1"},
			{dryRunBookNew.Title, "none", "-", "create + append", "1"},
			{dryRunBookAmbiguous.Title, "ambiguous", "-", "skip", "-"},
			{},
			{"Exact matches", "1"},
			{"Adoptable matches", "0"},
			{"New books", "1"},
			{"Ambiguous", "1"},
			{"Highlights to append", "2"}, // exact の 2 件のうち Sync Marker で既知の 1 件を除き、新規の 1 件を足す
		}
		got := planCells(t, stdout)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("run(sync --dry-run) stdout cells = %q, want %q", got, want)
		}
		assertAppendCountSum(t, got)
		// ambiguous の Book は Highlight を取りに行かず、ほかの Book も 1 回ずつしか取らない
		if want := []domain.Book{dryRunBookExact, dryRunBookNew}; !slices.Equal(b.hlBooks, want) {
			t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, want)
		}
		if !b.closed {
			t.Error("browser not closed")
		}
		// Notion への要求は Data Source の query と、ページがある exact の Book の子 block の読み取りだけで、ページ・Property・block を変える要求は無い
		assertRequests(t, requests(), "POST "+testDryRunQueryPath, "GET "+testDryRunBlocksPath)
		// state.db は作られず、data ディレクトリには run.lock しか増えない
		if got, want := dataDirEntries(t, p.DataDir), []string{"run.lock"}; !slices.Equal(got, want) {
			t.Errorf("data dir entries after sync = %v, want %v", got, want)
		}
	})

	t.Run("ASIN の無い Book は Highlight を取らずに失敗として数え、残りの Book の予定を出して exit 7 で終わる", func(t *testing.T) {
		summary := func(exact, newBooks, highlights string) [][]string {
			return [][]string{
				{},
				{"Exact matches", exact},
				{"Adoptable matches", "0"},
				{"New books", newBooks},
				{"Ambiguous", "0"},
				{"Highlights to append", highlights},
			}
		}
		header := []string{"Kindle Book", "Match Status", "Matched Notion Page", "Action", "Highlights to append"}
		tests := []struct {
			name        string
			books       []domain.Book
			wantCells   [][]string
			wantHLBooks []domain.Book
			wantStderr  string
			// Notion への要求（Book が 1 冊も残らなければ照合せず、exact の Book が残れば子 block も読む）
			wantRequests []string
		}{
			{
				name:  "ASIN の無い Book が混ざる",
				books: []domain.Book{dryRunBookExact, dryRunBookNoASIN, dryRunBookNew},
				wantCells: append([][]string{
					header,
					{dryRunBookExact.Title, "exact", "page-exact", "append", "1"},
					{dryRunBookNew.Title, "none", "-", "create + append", "1"},
				}, summary("1", "1", "2")...), // exact の 2 件のうち Sync Marker で既知の 1 件を除き、新規の 1 件を足す
				wantHLBooks:  []domain.Book{dryRunBookExact, dryRunBookNew},
				wantStderr:   "source format error: sync: highlights of 1 of 3 books could not be listed: b1-0123456789abcdef0123456789abcdef (book has no ASIN)\n",
				wantRequests: []string{"POST " + testDryRunQueryPath, "GET " + testDryRunBlocksPath},
			},
			{
				name:         "ASIN の無い Book だけ",
				books:        []domain.Book{dryRunBookNoASIN},
				wantCells:    append([][]string{header}, summary("0", "0", "0")...),
				wantHLBooks:  nil,
				wantStderr:   "source format error: sync: highlights of 1 of 1 books could not be listed: b1-0123456789abcdef0123456789abcdef (book has no ASIN)\n",
				wantRequests: nil,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, dryRunReplies())
				b := newDryRunBrowser()
				b.books = tt.books
				stubBrowser(t, b, nil)

				code, stdout, stderr := runSyncDryRun(t)
				if code != 7 {
					t.Errorf("run(sync --dry-run) = %d, want 7 (stderr = %q)", code, stderr)
				}
				if stderr != tt.wantStderr {
					t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
				}
				got := planCells(t, stdout)
				if !reflect.DeepEqual(got, tt.wantCells) {
					t.Errorf("run(sync --dry-run) stdout cells = %q, want %q", got, tt.wantCells)
				}
				assertAppendCountSum(t, got)
				if !slices.Equal(b.hlBooks, tt.wantHLBooks) {
					t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, tt.wantHLBooks)
				}
				assertRequests(t, requests(), tt.wantRequests...)
			})
		}
	})

	t.Run("認証切れ・同期先の組み立ての失敗・Notion の取得の失敗では予定を出さずに止まる", func(t *testing.T) {
		keychainErr := errors.New("keychain boom")
		tests := []struct {
			name         string
			configTOML   string
			token        string
			keychainErr  error // nil 以外なら Keychain の読み取りがこの失敗を返す
			replies      map[string]string
			pageHTML     []byte
			wantCode     int
			wantStderr   func(p config.Paths) string
			wantRequests []string
			wantHLBooks  int
		}{
			{
				name:       "認証切れでは exit 2",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				replies:    dryRunReplies(),
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
				replies:    map[string]string{testDryRunQueryPath: "not json"},
				wantCode:   4,
				wantStderr: func(config.Paths) string {
					return "Notion error: notion: POST " + testDryRunQueryPath + ": invalid response JSON\n"
				},
				wantRequests: []string{"POST " + testDryRunQueryPath},
			},
			{
				name:       "Notion の子 block の取得の失敗でも exit 4",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				replies:    map[string]string{testDryRunQueryPath: dryRunQueryBody(), testDryRunBlocksPath: "not json"},
				wantCode:   4,
				wantStderr: func(config.Paths) string {
					return "Notion error: notion: GET " + testDryRunBlocksPath + "?page_size=100: invalid response JSON\n"
				},
				wantRequests: []string{"POST " + testDryRunQueryPath, "GET " + testDryRunBlocksPath},
			},
			{
				// 採用方式と異なる prefix の Sync Marker は方式の混在として exit 6（§16.3）。Highlight は取らず、Notion にも書かない
				name:       "採用方式と異なる prefix の Sync Marker があれば exit 6",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				replies: map[string]string{
					testDryRunQueryPath:  dryRunQueryBody(),
					testDryRunBlocksPath: blockListBody(dryRunBlockJSON("block-1", "Distinctive Highlight Text\nkhs:h1-SECRETREST")),
				},
				wantCode: 6,
				wantStderr: func(config.Paths) string {
					return "local state / ID scheme error: notion page page-exact has a Sync Marker with Highlight ID prefix \"h1-\", but the adopted prefix is \"a-\"\n"
				},
				wantRequests: []string{"POST " + testDryRunQueryPath, "GET " + testDryRunBlocksPath},
			},
			{
				name:       "config.toml が無ければ exit 3",
				configTOML: "",
				token:      testDryRunToken,
				replies:    dryRunReplies(),
				wantCode:   3,
				wantStderr: func(p config.Paths) string {
					return "configuration / confirmation required: " + p.ConfigFile + " not found: run `kindle-highlight-sync init` to create it\n"
				},
			},
			{
				name:       "token が無ければ exit 3",
				configTOML: validConfigTOML,
				token:      "",
				replies:    dryRunReplies(),
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
				replies:     dryRunReplies(),
				wantCode:    1,
				wantStderr: func(config.Paths) string {
					return "general error: cannot read Notion token in Keychain (service kindle-highlight-sync, account notion-token)\n"
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, tt.configTOML)
				requests, _ := setupDryRunNotion(t, tt.replies)
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
				assertRequests(t, requests(), tt.wantRequests...)
			})
		}
	})

	t.Run("--dry-run を付けない Initial Sync の sync は予定の表を出さず、Notion には照合の読み取りのあとに実行の要求を出し、Highlight を予定と取得で 2 回取る", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		replies := dryRunReplies()
		replies[testPagesPath] = `{"object":"page","id":"page-created"}`
		requests, clientCalls := setupDryRunNotion(t, replies)
		b := newDryRunBrowser()
		stubBrowser(t, b, nil)

		// ambiguous の Book は skip するので exit 8
		code, stderr := runSync(t, "--yes")
		if want := "partial failure: sync: 1 books were not synced: " + dryRunBookAmbiguous.ID + " (ambiguous)\n"; code != 8 || stderr != want {
			t.Errorf("run(sync --yes) code = %d, stderr = %q, want 8 and %q", code, stderr, want)
		}
		// 予定の算出は ambiguous を除いた Book の Highlight を取り、従来の取得は全 Book を取る
		want := []domain.Book{dryRunBookExact, dryRunBookNew, dryRunBookExact, dryRunBookNew, dryRunBookAmbiguous}
		if !slices.Equal(b.hlBooks, want) {
			t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, want)
		}
		// 予定の算出と実行がそれぞれ同期先を組む
		if got := clientCalls(); got != 2 {
			t.Errorf("newNotionClient calls = %d, want 2", got)
		}
		assertRequests(t, requests(), "POST "+testDryRunQueryPath, "GET "+testDryRunBlocksPath, "POST "+testDryRunQueryPath, "POST "+testPagesPath)
	})

	t.Run("本物の sync が排他を持っている間の dry-run は待たずに exit 9 で終わる", func(t *testing.T) {
		setupXDG(t, validConfigTOML)
		_, clientCalls := setupDryRunNotion(t, dryRunReplies())
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
