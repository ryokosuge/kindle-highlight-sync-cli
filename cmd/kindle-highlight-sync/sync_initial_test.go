package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// Initial Sync の end-to-end の fixture。dryRunBooks に Adopt の候補を 1 冊足し、Create 1 冊（dryRunBookNew）・Adopt 1 冊になる。
const (
	testInitialAdoptBlocksPath = "/v1/blocks/page-adopt/children"
	testCreatedBlocksPath      = "/v1/blocks/page-created/children"
	testPagesPath              = "/v1/pages"
	wantInitialPrompt          = "1 books will be created.\n1 existing pages will be adopted.\n\nContinue? [y/N] "
	wantInitialDeclinedErr     = "configuration / confirmation required: initial sync was not confirmed\n"
	wantInitialNoTerminalErr   = "configuration / confirmation required: initial sync will create 1 books and adopt 1 existing pages: run sync with --yes to confirm without a terminal\n"
)

var (
	initialBookAdopt = domain.Book{ID: "B000000005", ASIN: "B000000005", Title: "Distinctive Adoptable Book"}
	initialBooks     = []domain.Book{dryRunBookExact, dryRunBookNew, initialBookAdopt}
	// ambiguous の Book を足したもの。予定の算出は ambiguous の Book の Highlight を取らず、実行は skip して exit 8 にする
	initialBooksWithAmbiguous = []domain.Book{dryRunBookExact, dryRunBookNew, dryRunBookAmbiguous, initialBookAdopt}
	// 予定の算出（BuildPlan）が Highlight を取る Book の順。ambiguous は取らない
	initialPlanHLBooks = []domain.Book{dryRunBookExact, dryRunBookNew, initialBookAdopt}
	// 照合の読み取りだけの要求。ページ・Property・block を変える要求（POST /v1/pages 等）は無い
	initialReadRequests = []string{"POST " + testDryRunQueryPath, "GET " + testDryRunBlocksPath, "GET " + testInitialAdoptBlocksPath}
	// 確認を通ったあとの実行の要求。同期先を組み直して全件を取り直し、exact の Book を更新し、none の Book を作成し、adoptable の Book を Adopt する
	// Highlight を持つ Book（exact・none・adoptable）のページには、既知を除いて追記する。exact は既知を読んでから追記し、作成したページは既知を読まずに追記し、Adopt したページは Adopt のあとに既知を読んで追記する
	initialApplyRequests = []string{"POST " + testDryRunQueryPath, "PATCH " + testPagesPath + "/page-exact", "GET " + testDryRunBlocksPath, "PATCH " + testDryRunBlocksPath,
		"POST " + testPagesPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testPagesPath + "/page-adopt", "GET " + testInitialAdoptBlocksPath, "PATCH " + testInitialAdoptBlocksPath}
	// 続く sync の実行の要求。exact / adopt のページの Highlight は State に保存済みなので Notion の一覧も追記も無く、同じ Data Source を返す Notion では none の Book をまた作成して追記する
	initialSecondApplyRequests = []string{"POST " + testDryRunQueryPath, "PATCH " + testPagesPath + "/page-exact",
		"POST " + testPagesPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testPagesPath + "/page-adopt"}
	initialSecondApplyRequestsNewBookFailed = []string{"POST " + testDryRunQueryPath, "PATCH " + testPagesPath + "/page-exact",
		"POST " + testPagesPath, "PATCH " + testPagesPath + "/page-adopt"}
	// none の Book の Highlight の取得が失敗したときの実行の要求。作成までで追記は無い
	initialApplyRequestsNewBookFailed = []string{"POST " + testDryRunQueryPath, "PATCH " + testPagesPath + "/page-exact", "GET " + testDryRunBlocksPath, "PATCH " + testDryRunBlocksPath,
		"POST " + testPagesPath, "PATCH " + testPagesPath + "/page-adopt", "GET " + testInitialAdoptBlocksPath, "PATCH " + testInitialAdoptBlocksPath}
	// 確認を通った run が Notion に届ける要求の全体
	initialAppliedRequests = slices.Concat(initialReadRequests, initialApplyRequests)
	// 1 冊だけの構造の異常（Book 単位の失敗）と、全 Book に共通の枠の異常（fatal）。形は sync_test.go の bookErr / frameErr と同じ
	errSourceFormatBook = fmt.Errorf("source: list highlights B000000003: page 1: row 1: %w",
		exitcode.New(exitcode.SourceFormat, fmt.Errorf("highlight id is missing: %w", source.ErrSourceFormat)))
	errHighlightsFrame = fmt.Errorf("source: list highlights B000000003: page 1: source: highlights page: %w: %w", source.ErrHighlightsFrame,
		exitcode.New(exitcode.SourceFormat, fmt.Errorf("next page token input is missing: %w", source.ErrSourceFormat)))
)

// initialReplies は dryRunReplies に Adopt の候補（Kindle ID が空で Title が一致するページ）を足した Notion の応答。
func initialReplies() map[string]string {
	replies := dryRunReplies()
	replies[testDryRunQueryPath] = queryBody(
		dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
		dryRunPageJSON("page-dup-1", "", dryRunBookAmbiguous.Title),
		dryRunPageJSON("page-dup-2", "", dryRunBookAmbiguous.Title),
		dryRunPageJSON("page-adopt", "", initialBookAdopt.Title),
	)
	replies[testInitialAdoptBlocksPath] = blockListBody()
	replies[testPagesPath] = `{"object":"page","id":"page-created"}`
	replies[testPagesPath+"/page-adopt"] = `{"object":"page","id":"page-adopt"}`
	return replies
}

// wantInitialRequests は Initial Sync の run が Notion に届ける要求。確認を通って記録した run は実行の要求まで届け、そうでなければ照合の読み取りだけ。
func wantInitialRequests(confirmed bool) []string {
	if confirmed {
		return initialAppliedRequests
	}
	return initialReadRequests
}

// newInitialBrowser は Notebook に到達し initialBooks を返す fake。
func newInitialBrowser() *fakeBrowser {
	b := newDryRunBrowser()
	b.books = initialBooks
	b.highlights = maps.Clone(dryRunHighlights)
	b.highlights[initialBookAdopt.ID] = []domain.Highlight{{ID: "a-HIGHLIGHT-5", BookID: initialBookAdopt.ID, Text: "Distinctive Highlight Five"}}
	return b
}

// stubSyncPrompter は newSyncPrompter を input を読む prompter に差し替える。
// terminal が true なら入力が端末であるものとして扱う（confirmInitialSync は term の有無しか見ないので、端末の代わりに /dev/null を置く）。
func stubSyncPrompter(t *testing.T, input string, terminal bool) {
	t.Helper()
	var tty *os.File
	if terminal {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		tty = f
	}
	oldPrompter := newSyncPrompter
	newSyncPrompter = func(cmd *cobra.Command) *prompter {
		cmd.SetIn(strings.NewReader(input))
		p := newPrompter(cmd)
		p.term = tty
		return p
	}
	t.Cleanup(func() {
		newSyncPrompter = oldPrompter
		if tty != nil {
			_ = tty.Close()
		}
	})
}

// initialSyncRecorded は state.db に initial_sync_completed_at が記録されているかを返す。state.db が無ければ false。
func initialSyncRecorded(t *testing.T, p config.Paths) bool {
	t.Helper()
	_, ok, err := state.NewSQLiteStateStore(p.StateFile()).InitialSyncCompletedAt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// fakeStateStore は本物の State を包み、Initial Sync の読み取りと記録だけを失敗させる。
type fakeStateStore struct {
	state.StateStore
	readErr error // nil 以外なら InitialSyncCompletedAt がこれを返す
	markErr error // nil 以外なら MarkInitialSyncCompleted がこれを返す
	marked  int
}

func (f *fakeStateStore) InitialSyncCompletedAt(ctx context.Context) (time.Time, bool, error) {
	if f.readErr != nil {
		return time.Time{}, false, f.readErr
	}
	return f.StateStore.InitialSyncCompletedAt(ctx)
}

func (f *fakeStateStore) MarkInitialSyncCompleted(ctx context.Context, at time.Time) error {
	f.marked++
	if f.markErr != nil {
		return f.markErr
	}
	return f.StateStore.MarkInitialSyncCompleted(ctx, at)
}

// stubStateStore は newStateStore を、本物の state.db を包む fakeStateStore に差し替える。
func stubStateStore(t *testing.T, readErr, markErr error) *fakeStateStore {
	t.Helper()
	f := &fakeStateStore{readErr: readErr, markErr: markErr}
	oldStore := newStateStore
	newStateStore = func(p config.Paths) state.StateStore {
		f.StateStore = state.NewSQLiteStateStore(p.StateFile())
		return f
	}
	t.Cleanup(func() { newStateStore = oldStore })
	return f
}

func TestSyncInitial(t *testing.T) {
	t.Run("端末の入力では Create / Adopt の件数と Continue? [y/N] を出し、N なら exit 3 で終わって Notion を変えず記録せず、y なら進んで記録する", func(t *testing.T) {
		tests := []struct {
			name         string
			input        string
			wantCode     int
			wantStderr   string
			wantHLBooks  []domain.Book
			wantRecorded bool
		}{
			{
				name:        "N で答えると exit 3",
				input:       "N\n",
				wantCode:    3,
				wantStderr:  wantInitialPrompt + wantInitialDeclinedErr,
				wantHLBooks: initialPlanHLBooks,
			},
			{
				name:        "空行（既定の No）でも exit 3",
				input:       "\n",
				wantCode:    3,
				wantStderr:  wantInitialPrompt + wantInitialDeclinedErr,
				wantHLBooks: initialPlanHLBooks,
			},
			{
				name:         "y で答えると取得に進んで exit 0",
				input:        "y\n",
				wantCode:     0,
				wantStderr:   wantInitialPrompt,
				wantHLBooks:  slices.Concat(initialPlanHLBooks, initialBooks),
				wantRecorded: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				stubSyncPrompter(t, tt.input, true)
				b := newInitialBrowser()
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				if !slices.Equal(b.hlBooks, tt.wantHLBooks) {
					t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, tt.wantHLBooks)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
				assertRequests(t, requests(), wantInitialRequests(tt.wantRecorded)...)
				if got := initialSyncRecorded(t, p); got != tt.wantRecorded {
					t.Errorf("initial_sync_completed_at recorded = %v, want %v", got, tt.wantRecorded)
				}
			})
		}
	})

	t.Run("入力が端末でなければ問いを出さずに exit 3 で終わって記録せず、--yes なら確認を省いて進む", func(t *testing.T) {
		tests := []struct {
			name         string
			args         []string
			wantCode     int
			wantStderr   string
			wantHLBooks  []domain.Book
			wantRecorded bool
		}{
			{
				name:        "--yes が無ければ exit 3",
				wantCode:    3,
				wantStderr:  wantInitialNoTerminalErr,
				wantHLBooks: initialPlanHLBooks,
			},
			{
				name:         "--yes があれば exit 0",
				args:         []string{"--yes"},
				wantCode:     0,
				wantHLBooks:  slices.Concat(initialPlanHLBooks, initialBooks),
				wantRecorded: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				// 端末でない入力に y を流しても読まれない
				stubSyncPrompter(t, "y\n", false)
				b := newInitialBrowser()
				stubBrowser(t, b, nil)

				code, stderr := runSync(t, tt.args...)
				if code != tt.wantCode {
					t.Errorf("run(sync %s) code = %d, want %d", strings.Join(tt.args, " "), code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync %s) stderr = %q, want %q", strings.Join(tt.args, " "), stderr, tt.wantStderr)
				}
				if !slices.Equal(b.hlBooks, tt.wantHLBooks) {
					t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, tt.wantHLBooks)
				}
				assertRequests(t, requests(), wantInitialRequests(tt.wantRecorded)...)
				if got := initialSyncRecorded(t, p); got != tt.wantRecorded {
					t.Errorf("initial_sync_completed_at recorded = %v, want %v", got, tt.wantRecorded)
				}
			})
		}
	})

	t.Run("確認を通過した run は success でも Book 単位の失敗（exit 7）でも記録し、続く sync では予定の算出も確認も行わない", func(t *testing.T) {
		tests := []struct {
			name         string
			books        []domain.Book
			highlightErr map[string]error
			wantCode     int
			wantStderr   string
		}{
			{
				name:     "エラー無しで終わる（success）",
				books:    initialBooks,
				wantCode: 0,
			},
			{
				// ASIN の無い Book は予定の算出から除かれ、取得のループで失敗に数えられる
				name:       "ASIN の無い Book があって exit 7 で終わる（partial）",
				books:      slices.Concat(initialBooks, []domain.Book{dryRunBookNoASIN}),
				wantCode:   7,
				wantStderr: "source format error: sync: highlights of 1 of 4 books could not be listed: " + dryRunBookNoASIN.ID + " (book has no ASIN)\n",
			},
			{
				// 予定の算出では取れた Book の Highlight のページが、実行では構造の異常になる
				name:         "1 冊の Highlight のページだけが構造の異常で exit 7 で終わる（partial）",
				books:        initialBooks,
				highlightErr: map[string]error{dryRunBookNew.ID: errSourceFormatBook},
				wantCode:     7,
				wantStderr:   "source format error: sync: highlights of 1 of 3 books could not be listed: " + dryRunBookNew.ID + " (unexpected notebook structure)\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				stubSyncPrompter(t, "y\n", true)
				b := newInitialBrowser()
				b.books = tt.books
				b.laterHLErr = tt.highlightErr
				stubBrowser(t, b, nil)

				// 記録は閉じた後に行うので、Close の時点ではまだ記録されていない
				var recordedAtClose bool
				b.onClose = func() { recordedAtClose = initialSyncRecorded(t, p) }

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if want := wantInitialPrompt + tt.wantStderr; stderr != want {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, want)
				}
				if recordedAtClose {
					t.Error("initial_sync_completed_at recorded before the browser was closed")
				}
				if !initialSyncRecorded(t, p) {
					t.Fatal("initial_sync_completed_at not recorded")
				}
				firstApply, secondApply := initialApplyRequests, initialSecondApplyRequests
				if tt.highlightErr != nil {
					firstApply, secondApply = initialApplyRequestsNewBookFailed, initialSecondApplyRequestsNewBookFailed
				}
				assertRequests(t, requests(), slices.Concat(initialReadRequests, firstApply)...)

				// 2 回目は Initial Sync ではないので、予定の算出も問いも出さず、Highlight は実行で 1 回だけ取る
				b2 := newInitialBrowser()
				b2.books = tt.books
				b2.highlightErr = tt.highlightErr
				stubBrowser(t, b2, nil)
				code, stderr = runSync(t)
				if code != tt.wantCode {
					t.Errorf("2nd run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("2nd run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				if !slices.Equal(b2.hlBooks, withASIN(tt.books)) {
					t.Errorf("2nd ListHighlights() books = %+v, want %+v", b2.hlBooks, withASIN(tt.books))
				}
				assertRequests(t, requests(), slices.Concat(initialReadRequests, firstApply, secondApply)...)
			})
		}
	})

	t.Run("予定の算出で 1 冊の Highlight のページだけが構造の異常なら、確認の前に exit 7 で止まり Notion に書かず記録しない", func(t *testing.T) {
		tests := []struct {
			name         string
			highlightErr map[string]error
			wantCode     int
			wantStderr   string
		}{
			{
				name:         "none の Book の取得が構造の異常",
				highlightErr: map[string]error{dryRunBookNew.ID: errSourceFormatBook},
				wantCode:     7,
				wantStderr:   "source format error: sync: highlights of 1 of 3 books could not be listed: " + dryRunBookNew.ID + " (unexpected notebook structure)\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				stubSyncPrompter(t, "y\n", true)
				b := newInitialBrowser()
				b.highlightErr = tt.highlightErr
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				assertRequests(t, requests(), initialReadRequests...)
				if initialSyncRecorded(t, p) {
					t.Error("initial_sync_completed_at recorded, want not recorded")
				}
			})
		}
	})

	t.Run("設定エラー（exit 3）・認証切れ（exit 2）・全 Book に共通の失敗・ブラウザを閉じる失敗（exit 5）で終わった run は記録しない", func(t *testing.T) {
		closeErr := exitcode.New(exitcode.Browser, errors.New("source: close browser: boom"))
		tests := []struct {
			name         string
			configTOML   string
			token        string
			pageHTML     []byte
			highlightErr map[string]error
			closeErr     error
			wantCode     int
			wantStderr   func(p config.Paths) string
			wantRequests []string
		}{
			{
				name:       "config.toml が無ければ exit 3",
				configTOML: "",
				token:      testDryRunToken,
				wantCode:   3,
				wantStderr: func(p config.Paths) string {
					return "configuration / confirmation required: " + p.ConfigFile + " not found: run `kindle-highlight-sync init` to create it\n"
				},
			},
			{
				name:       "token が無ければ exit 3",
				configTOML: validConfigTOML,
				token:      "",
				wantCode:   3,
				wantStderr: func(config.Paths) string {
					return "configuration / confirmation required: notion token not found in KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN or Keychain: run `kindle-highlight-sync init` to save it\n"
				},
			},
			{
				name:       "認証切れでは exit 2",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				pageHTML:   []byte(`<form name="signIn"><input name="email"></form>`),
				wantCode:   2,
				wantStderr: func(config.Paths) string {
					return "Amazon authentication required: sync: a login form is present at \"read.amazon.co.jp/notebook\" without the Kindle Notebook library; run `kindle-highlight-sync login` to sign in again\n"
				},
			},
			{
				// 確認を通過しても、全 Book に共通の失敗（Highlight の frame が無い）は partial ではなく fatal
				name:         "確認のあとの全 Book に共通の失敗では exit 7 でも記録しない",
				configTOML:   validConfigTOML,
				token:        testDryRunToken,
				highlightErr: map[string]error{dryRunBookNew.ID: errHighlightsFrame},
				wantCode:     7,
				wantStderr: func(config.Paths) string {
					return "source format error: " + errHighlightsFrame.Error() + "\n"
				},
				wantRequests: slices.Concat(initialReadRequests, initialApplyRequests[:5]), // 作成したあとの取得で止まる
			},
			{
				// 取得を終えても Chrome を閉じるのに失敗した run は success ではないので記録せず、exit 5 はそのまま
				name:       "確認のあとにブラウザを閉じるのに失敗すると exit 5 で記録しない",
				configTOML: validConfigTOML,
				token:      testDryRunToken,
				closeErr:   closeErr,
				wantCode:   5,
				wantStderr: func(config.Paths) string {
					return "browser error: source: close browser: boom\n"
				},
				wantRequests: initialAppliedRequests,
			},
			{
				// Book 単位の失敗と閉じる失敗が重なった run も記録しない。返すのは本体の失敗（exit 7）
				name:         "Book 単位の失敗のあとにブラウザを閉じるのに失敗すると exit 7 のまま記録しない",
				configTOML:   validConfigTOML,
				token:        testDryRunToken,
				highlightErr: map[string]error{dryRunBookNew.ID: errSourceFormatBook},
				closeErr:     closeErr,
				wantCode:     7,
				wantStderr: func(config.Paths) string {
					return "source format error: sync: highlights of 1 of 3 books could not be listed: " + dryRunBookNew.ID + " (unexpected notebook structure)\n"
				},
				wantRequests: slices.Concat(initialReadRequests, initialApplyRequestsNewBookFailed),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, tt.configTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				t.Setenv(secrets.EnvNotionToken, tt.token)
				stubSyncPrompter(t, "y\n", true)
				b := newInitialBrowser()
				if tt.pageHTML != nil {
					b.pageHTML = tt.pageHTML
				}
				b.laterHLErr = tt.highlightErr // 予定の算出では取れ、実行で失敗する
				b.closeErr = tt.closeErr
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				want := tt.wantStderr(p)
				if tt.wantRequests != nil {
					want = wantInitialPrompt + want
				}
				if stderr != want {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, want)
				}
				assertRequests(t, requests(), tt.wantRequests...)
				if initialSyncRecorded(t, p) {
					t.Error("initial_sync_completed_at recorded, want not recorded")
				}
			})
		}
	})

	t.Run("Initial Sync でも Create と Adopt が 0 件なら確認を出さずに進み、記録する", func(t *testing.T) {
		for _, terminal := range []bool{true, false} {
			t.Run(map[bool]string{true: "端末の入力", false: "端末でない入力で --yes なし"}[terminal], func(t *testing.T) {
				p := setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				// 問いが出れば N で止まる
				stubSyncPrompter(t, "N\n", terminal)
				b := newInitialBrowser()
				b.books = []domain.Book{dryRunBookExact}
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != 0 || stderr != "" {
					t.Errorf("run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
				}
				want := []domain.Book{dryRunBookExact, dryRunBookExact}
				if !slices.Equal(b.hlBooks, want) {
					t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, want)
				}
				// 作成・Adopt する Book が無くても、実行は同期先を組み直して照合し、exact の Book を更新する
				assertRequests(t, requests(), "POST "+testDryRunQueryPath, "GET "+testDryRunBlocksPath, "POST "+testDryRunQueryPath, "PATCH "+testPagesPath+"/page-exact", "GET "+testDryRunBlocksPath, "PATCH "+testDryRunBlocksPath)
				if !initialSyncRecorded(t, p) {
					t.Error("initial_sync_completed_at not recorded")
				}
			})
		}
	})

	t.Run("--dry-run は確認も記録もせず、state.db を作らない", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		setupDryRunNotion(t, initialReplies())
		stubSyncPrompter(t, "N\n", true)
		store := stubStateStore(t, errors.New("must not be read"), nil)
		stubBrowser(t, newInitialBrowser(), nil)

		code, stdout, stderr := runSyncDryRun(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync --dry-run) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if stdout == "" {
			t.Error("run(sync --dry-run) stdout = empty, want the plan")
		}
		if store.marked != 0 {
			t.Errorf("MarkInitialSyncCompleted calls = %d, want 0", store.marked)
		}
		if got := dataDirEntries(t, p.DataDir); !slices.Equal(got, []string{"run.lock"}) {
			t.Errorf("data dir entries = %q, want [run.lock]", got)
		}
	})

	t.Run("State の読み取りと記録の失敗", func(t *testing.T) {
		stateErr := exitcode.New(exitcode.LocalState, errors.New("state.db: boom"))
		tests := []struct {
			name       string
			readErr    error
			markErr    error
			books      []domain.Book
			wantCode   int
			wantStderr string
		}{
			{
				name:       "initial_sync_completed_at を読めなければ予定を出さずに exit 6",
				readErr:    stateErr,
				books:      initialBooks,
				wantCode:   6,
				wantStderr: "local state / ID scheme error: state.db: boom\n",
			},
			{
				name:       "本体が成功して記録に失敗すると exit 6",
				markErr:    stateErr,
				books:      initialBooks,
				wantCode:   6,
				wantStderr: wantInitialPrompt + "local state / ID scheme error: state.db: boom\n",
			},
			{
				name:       "Book 単位の失敗（exit 7）は記録の失敗より優先する",
				markErr:    stateErr,
				books:      slices.Concat(initialBooks, []domain.Book{dryRunBookNoASIN}),
				wantCode:   7,
				wantStderr: wantInitialPrompt + "source format error: sync: highlights of 1 of 4 books could not be listed: " + dryRunBookNoASIN.ID + " (book has no ASIN)\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupXDG(t, validConfigTOML)
				requests, _ := setupDryRunNotion(t, initialReplies())
				stubSyncPrompter(t, "y\n", true)
				store := stubStateStore(t, tt.readErr, tt.markErr)
				b := newInitialBrowser()
				b.books = tt.books
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				if tt.readErr != nil {
					if len(b.hlBooks) != 0 || store.marked != 0 {
						t.Errorf("ListHighlights() books = %+v, MarkInitialSyncCompleted calls = %d, want none", b.hlBooks, store.marked)
					}
					assertRequests(t, requests())
					return
				}
				if store.marked != 1 {
					t.Errorf("MarkInitialSyncCompleted calls = %d, want 1", store.marked)
				}
			})
		}
	})
}

// withASIN は ASIN のある Book だけを返す（取得のループが ListHighlights に渡す Book）。
func withASIN(books []domain.Book) []domain.Book {
	var out []domain.Book
	for _, b := range books {
		if b.ASIN != "" {
			out = append(out, b)
		}
	}
	return out
}
