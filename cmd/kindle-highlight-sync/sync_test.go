package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/lock"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

const (
	wantSyncShort         = "Kindle の Highlight と Note を Notion に同期する"
	wantSyncHeadedUsage   = "画面付きの Chrome で同期する（既定は headless）"
	wantSyncDryRunUsage   = "Notion と State を変えずに、Book の照合の結果と追記する Highlight の数だけを出す"
	wantSyncFullUsage     = "State の記録に関わらず全 Book の Highlight を取って同期する（既定は変化のある Book と未同期の Book だけ）"
	wantSyncYesUsage      = "Initial Sync の Create / Adopt の確認を省く（入力が端末でないときに要る）"
	wantAlreadyRunningErr = "already running: another kindle-highlight-sync process is running\n"
)

// stubReachedBrowser は Notebook に到達する fake に openBrowser を差し替える。CI に Chrome は無いので、sync を走らせるケースは必ず差し替える。
func stubReachedBrowser(t *testing.T) (*fakeBrowser, *browserStub) {
	t.Helper()
	b := &fakeBrowser{reached: true}
	return b, stubBrowser(t, b, nil)
}

// setupSyncedXDG は setupXDG に加えて Initial Sync 済みの State（initial_sync_completed_at あり）を置く。
// 通常の sync も作成・Adopt のために Notion につなぐので、configTOML が空なら validConfigTOML を置き、
// 照合は空の Data Source、書き込みは成功する Notion の代役（stubWritableNotion）に向ける。
func setupSyncedXDG(t *testing.T, configTOML string) config.Paths {
	t.Helper()
	if configTOML == "" {
		configTOML = validConfigTOML
	}
	p := setupXDG(t, configTOML)
	stubWritableNotion(t)
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.NewSQLiteStateStore(p.StateFile()).MarkInitialSyncCompleted(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	return p
}

// stubWritableNotion は token を環境変数に置き、Data Source が空で、ページの作成・更新が成功する Notion の代役に newNotionClient を向ける。
// 受けた要求を返すので、書き込みの要求が届いたかを確かめられる。
func stubWritableNotion(t *testing.T) func() []recordedRequest {
	t.Helper()
	requests, _ := setupDryRunNotion(t, map[string]string{
		testDryRunQueryPath: queryBody(),
		"/v1/pages":         `{"object":"page","id":"page-written"}`,
	})
	return requests
}

// runSync は sync を args 付きで走らせる。標準出力は実行の結果の件数なので、返さず確かめない。
func runSync(t *testing.T, args ...string) (code int, stderr string) {
	t.Helper()
	var stdout, stderrBuf bytes.Buffer
	code = run(append([]string{"sync"}, args...), &stdout, &stderrBuf)
	return code, stderrBuf.String()
}

// captureProcessOutput は f の間 os.Stdout / os.Stderr をファイルに差し替え、そこに書かれた内容を返す。
// run に渡した writer を通らない出力（fmt.Print や log）も、標準出力・標準エラーに出ていないことを確かめるために使う。
func captureProcessOutput(t *testing.T, f func()) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	// 標準 logger は起動時の os.Stderr を持ち続けるので、出力先も差し替える
	origOut, origErr, origLog := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = outFile, errFile
	log.SetOutput(errFile)
	func() {
		defer func() {
			os.Stdout, os.Stderr = origOut, origErr
			log.SetOutput(origLog)
		}()
		f()
	}()
	for _, file := range []*os.File{outFile, errFile} {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	out, err := os.ReadFile(outFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(errOut)
}

func assertSyncSucceeded(t *testing.T, code int, stderr string, lockFile string) {
	t.Helper()
	if code != 0 {
		t.Errorf("run(sync) code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("run(sync) stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(lockFile); err != nil {
		t.Errorf("Stat(%q) error = %v, want run.lock to exist", lockFile, err)
	}
}

func TestSync(t *testing.T) {
	t.Run("排他の内側で login と同じ browser-profile を開き、Notebook に到達すると exit 0 で終わる", func(t *testing.T) {
		tests := []struct {
			name         string
			args         []string
			wantHeadless bool
		}{
			{name: "--headed を付けなければ headless で開く", args: []string{"sync"}, wantHeadless: true},
			{name: "--headed を付けると画面付きで開く", args: []string{"sync", "--headed"}, wantHeadless: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupSyncedXDG(t, "")
				b, s := stubReachedBrowser(t)

				var stdout, stderr bytes.Buffer
				code := run(tt.args, &stdout, &stderr)
				if got, want := planCells(t, stdout.String()), withTargetCount(applyCells("0", "0", "0", "0", "0"), "0", "0"); !reflect.DeepEqual(got, want) {
					t.Errorf("run(%v) stdout cells = %q, want %q", tt.args, got, want)
				}
				assertSyncSucceeded(t, code, stderr.String(), filepath.Join(p.DataDir, "run.lock"))
				wantOpts := source.Options{ProfileDir: filepath.Join(p.DataDir, "browser-profile"), Headless: tt.wantHeadless}
				if s.opened != 1 || s.opts != wantOpts {
					t.Errorf("openBrowser called %d times with %+v, want once with %+v", s.opened, s.opts, wantOpts)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
			})
		}
	})

	t.Run("Notebook に到達し認証を確かめたあと Book 一覧を最後まで取得し、Book ごとに全 Highlight を取得して exit 0 で終わり、Book と Highlight を出力しない", func(t *testing.T) {
		setupSyncedXDG(t, "")
		books := []domain.Book{
			{ID: "book-id-1", ASIN: "B000000001", Title: "Distinctive Book One"},
			{ID: "book-id-2", ASIN: "B000000002", Title: "Distinctive Book Two"},
			{ID: "book-id-3", ASIN: "B000000003", Title: "Distinctive Book Three"},
		}
		b := &fakeBrowser{
			reached: true,
			books:   books,
			highlights: map[string][]domain.Highlight{
				"book-id-1": {
					{ID: "a-HIGHLIGHT-1", BookID: "book-id-1", Text: "Distinctive Highlight Text", Note: "Distinctive Note"},
					{ID: "a-NOTE-2", BookID: "book-id-1", Note: "Distinctive Note Only"},
				},
				"book-id-3": {{ID: "a-HIGHLIGHT-3", BookID: "book-id-3", Text: "Distinctive Highlight Three"}},
			},
			pageURL:  "https://read.amazon.co.jp/notebook",
			pageHTML: []byte(`<div id="kp-notebook-library"></div>`),
		}
		stubBrowser(t, b, nil)

		// run の stdout / stderr とプロセスの標準出力・標準エラーがすべて空なので、Highlight の本文・Note・件数はどこにも出ない
		var code int
		var stderr string
		osStdout, osStderr := captureProcessOutput(t, func() { code, stderr = runSync(t) })
		if osStdout != "" || osStderr != "" {
			t.Errorf("process stdout = %q, stderr = %q, want both empty", osStdout, osStderr)
		}
		if code != 0 {
			t.Errorf("run(sync) code = %d, want 0", code)
		}
		if stderr != "" {
			t.Errorf("run(sync) stderr = %q, want empty", stderr)
		}
		want := []string{"OpenNotebook", "Page", "ListBooks", "ListHighlights", "ListHighlights", "ListHighlights", "Close"}
		if !slices.Equal(b.calls, want) {
			t.Errorf("calls = %v, want %v", b.calls, want)
		}
		if b.listCalled != 1 {
			t.Errorf("ListBooks() called %d times, want 1", b.listCalled)
		}
		if !slices.Equal(b.hlBooks, books) {
			t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, books)
		}
	})

	t.Run("Book ごとの Highlight の取得の失敗", func(t *testing.T) {
		book1 := domain.Book{ID: "book-id-1", ASIN: "B000000001", Title: "Distinctive Book One"}
		book2 := domain.Book{ID: "book-id-2", ASIN: "B000000002", Title: "Distinctive Book Two"}
		book3 := domain.Book{ID: "book-id-3", ASIN: "B000000003", Title: "Distinctive Book Three"}
		// ASIN が無い Book。Book ID は §13 の b1- の代替値で非空
		bookNoASIN := domain.Book{ID: "b1-0123456789abcdef0123456789abcdef", Title: "Distinctive Book Without ASIN", Author: "Distinctive Author"}
		// 1 冊だけの構造の異常（行の id が読めない等）。source は newFormatError で SourceFormat にして Book の ASIN とページを前置する
		bookErr := fmt.Errorf("source: list highlights B000000002: page 1: row 1: %w",
			exitcode.New(exitcode.SourceFormat, fmt.Errorf("highlight id is missing: %w", source.ErrSourceFormat)))
		// 全 Book に共通の枠の異常。source は ErrHighlightsFrame を併記する
		frameErr := fmt.Errorf("source: list highlights B000000002: page 1: source: highlights page: %w: %w", source.ErrHighlightsFrame,
			exitcode.New(exitcode.SourceFormat, fmt.Errorf("next page token input is missing: %w", source.ErrSourceFormat)))

		tests := []struct {
			name         string
			books        []domain.Book
			highlightErr map[string]error
			wantCode     int
			wantStderr   string
			wantHLBooks  []domain.Book // ListHighlights に渡った Book の順
		}{
			{
				name:         "1 冊だけが構造の異常なら残りの Book を取り終えてから exit 8 で終わり、その Book を Book ID と種類で区別する",
				books:        []domain.Book{book1, book2, book3},
				highlightErr: map[string]error{"book-id-2": bookErr},
				wantCode:     8,
				wantStderr:   "partial failure: sync: 1 books were not synced: book-id-2 (unexpected notebook structure)\n",
				wantHLBooks:  []domain.Book{book1, book2, book3},
			},
			{
				name:        "ASIN が無い Book には ListHighlights を呼ばず、残りの Book を取り終えてから exit 8 で終わり、その Book を Book ID と「ASIN が無い」で区別する",
				books:       []domain.Book{book1, bookNoASIN, book3},
				wantCode:    8,
				wantStderr:  "partial failure: sync: 1 books were not synced: b1-0123456789abcdef0123456789abcdef (book has no ASIN)\n",
				wantHLBooks: []domain.Book{book1, book3},
			},
			{
				name:         "複数の Book が失敗したら Book 一覧の順にすべて区別する",
				books:        []domain.Book{book1, bookNoASIN, book2, book3},
				highlightErr: map[string]error{"book-id-3": bookErr},
				wantCode:     8,
				wantStderr:   "partial failure: sync: 2 books were not synced: b1-0123456789abcdef0123456789abcdef (book has no ASIN); book-id-3 (unexpected notebook structure)\n",
				wantHLBooks:  []domain.Book{book1, book2, book3},
			},
			{
				name:         "全 Book に共通の枠の異常なら Book ごとに続けずにすぐ exit 7 で止まる",
				books:        []domain.Book{book1, book2, book3},
				highlightErr: map[string]error{"book-id-2": frameErr},
				wantCode:     7,
				wantStderr:   "source format error: source: list highlights B000000002: page 1: source: highlights page: highlights frame is unreadable: next page token input is missing: unexpected notebook structure\n",
				wantHLBooks:  []domain.Book{book1, book2},
			},
			{
				name:         "種類を持たない失敗はその時点でそのまま返す",
				books:        []domain.Book{book1, book2, book3},
				highlightErr: map[string]error{"book-id-2": errors.New("source: list highlights B000000002: page 2: boom")},
				wantCode:     1,
				wantStderr:   "general error: source: list highlights B000000002: page 2: boom\n",
				wantHLBooks:  []domain.Book{book1, book2},
			},
			{
				name:         "ctx の取消は失敗を集めている途中でもその時点で返す",
				books:        []domain.Book{book1, bookNoASIN, book2, book3},
				highlightErr: map[string]error{"book-id-2": fmt.Errorf("source: list highlights B000000002: page 1: %w", context.Canceled)},
				wantCode:     1,
				wantStderr:   "general error: source: list highlights B000000002: page 1: context canceled\n",
				wantHLBooks:  []domain.Book{book1, book2},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupSyncedXDG(t, "")
				b := &fakeBrowser{
					reached: true,
					books:   tt.books,
					highlights: map[string][]domain.Highlight{
						"book-id-1": {{ID: "a-HIGHLIGHT-1", BookID: "book-id-1", Text: "Distinctive Highlight Text", Note: "Distinctive Note"}},
						"book-id-3": {{ID: "a-HIGHLIGHT-3", BookID: "book-id-3", Text: "Distinctive Highlight Three"}},
					},
					highlightErr: tt.highlightErr,
				}
				stubBrowser(t, b, nil)

				var code int
				var stderr string
				osStdout, osStderr := captureProcessOutput(t, func() { code, stderr = runSync(t) })
				if osStdout != "" || osStderr != "" {
					t.Errorf("process stdout = %q, stderr = %q, want both empty", osStdout, osStderr)
				}
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				// 失敗した Book の Title・Highlight の本文・Note はどこにも出ない（Title 等はすべて Distinctive で始まる）
				if strings.Contains(stderr, "Distinctive") {
					t.Errorf("run(sync) stderr = %q, want no Title / Text / Note", stderr)
				}
				if !slices.Equal(b.hlBooks, tt.wantHLBooks) {
					t.Errorf("ListHighlights() books = %+v, want %+v", b.hlBooks, tt.wantHLBooks)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
			})
		}
	})

	t.Run("Book 一覧の取得に失敗したら exit 0 で終わらず、その失敗をそのまま返す", func(t *testing.T) {
		tests := []struct {
			name       string
			listErr    error
			wantCode   int
			wantStderr string
		}{
			{
				name:       "種類を持たない失敗",
				listErr:    errors.New("source: list books: page 2: boom"),
				wantCode:   1,
				wantStderr: "general error: source: list books: page 2: boom\n",
			},
			{
				name:       "SourceFormat の失敗",
				listErr:    exitcode.New(exitcode.SourceFormat, errors.New("source: list books: page 1: ...")),
				wantCode:   7,
				wantStderr: "source format error: source: list books: page 1: ...\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupXDG(t, "")
				// 失敗と一緒に Book を返しても、Highlight の取得に進まないことを確かめる
				b := &fakeBrowser{reached: true, listErr: tt.listErr, books: []domain.Book{{ID: "book-id-1"}}}
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
				if b.listCalled != 1 {
					t.Errorf("ListBooks() called %d times, want 1", b.listCalled)
				}
				if len(b.hlBooks) != 0 {
					t.Errorf("ListHighlights() books = %+v, want none", b.hlBooks)
				}
			})
		}
	})

	t.Run("Book 一覧が構造の異常を返すと exit 7 で終わり、見つからなかった構造を 1 行出す", func(t *testing.T) {
		// internal/source の listBooks が返す実際の形（newFormatError で包まれた SourceFormat の種類）
		listErr := exitcode.New(exitcode.SourceFormat, fmt.Errorf("source: list books: page 1: %w",
			fmt.Errorf("source: library page: notebook root container is missing: %w", source.ErrSourceFormat)))
		const wantErr = "source format error: source: list books: page 1: source: library page: notebook root container is missing: unexpected notebook structure\n"
		tests := []struct {
			name     string
			pageURL  string
			pageHTML []byte
		}{
			{name: "認証済みと判定したあと", pageURL: "https://read.amazon.co.jp/notebook", pageHTML: []byte(`<div id="kp-notebook-library"></div>`)},
			{name: "認証が判定不能のまま取得に進んでも exit 2 にならない", pageURL: "https://read.amazon.co.jp/notebook", pageHTML: []byte("<p>notebook</p>")},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, "")
				b := &fakeBrowser{reached: true, listErr: listErr, pageURL: tt.pageURL, pageHTML: tt.pageHTML}
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != 7 {
					t.Errorf("run(sync) code = %d, want 7", code)
				}
				if stderr != wantErr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, wantErr)
				}
				// 見つからなかった構造の 1 行だけを出し、HTML の断片や Book・クエリの値を含めない（仕様書 §74）
				for _, ng := range []string{"<", "Title", "Author", "library=list&token="} {
					if strings.Contains(stderr, ng) {
						t.Errorf("run(sync) stderr = %q, must not contain %q", stderr, ng)
					}
				}
				if !b.closed {
					t.Error("browser not closed")
				}
				if b.listCalled != 1 {
					t.Errorf("ListBooks() called %d times, want 1", b.listCalled)
				}
				// 構造の異常で終わっても排他（run.lock）は解放される
				lockFile := filepath.Join(p.DataDir, "run.lock")
				l, err := lock.Acquire(lockFile)
				if err != nil {
					t.Fatalf("lock.Acquire(%q) after exit 7 = %v, want released", lockFile, err)
				}
				if err := l.Release(); err != nil {
					t.Fatalf("Release(%q) = %v", lockFile, err)
				}
			})
		}
	})

	t.Run("Notebook を開く action には 1 分の期限が付き、Open と Book 一覧・Highlight の取得に渡す ctx には付かない", func(t *testing.T) {
		setupSyncedXDG(t, "")
		b := &fakeBrowser{reached: true, books: []domain.Book{{ID: "book-id-1", ASIN: "B000000001"}, {ID: "book-id-2", ASIN: "B000000002"}}}
		// stubBrowser は渡った ctx を見ないので、fake を返す関数の中で期限の無いことを確かめる
		orig := openBrowser
		openBrowser = func(ctx context.Context, _ source.Options) (browser, error) {
			if d, ok := ctx.Deadline(); ok {
				t.Errorf("openBrowser ctx has deadline %v, want none", d)
			}
			return b, nil
		}
		t.Cleanup(func() { openBrowser = orig })

		if code, stderr := runSync(t); code != 0 {
			t.Fatalf("run(sync) code = %d, stderr = %q, want 0", code, stderr)
		}
		if b.openDeadline.IsZero() {
			t.Fatal("OpenNotebook ctx has no deadline, want 1m")
		}
		if wait := b.openDeadline.Sub(b.openCalledAt); wait > time.Minute || wait < 50*time.Second {
			t.Errorf("OpenNotebook deadline is %v ahead, want 1m", wait)
		}
		if b.listCalled != 1 {
			t.Errorf("ListBooks() called %d times, want 1", b.listCalled)
		}
		if !b.listDeadline.IsZero() {
			t.Errorf("ListBooks ctx has deadline %v, want none", b.listDeadline)
		}
		if want := []time.Time{{}, {}}; !slices.Equal(b.hlDeadlines, want) {
			t.Errorf("ListHighlights ctx deadlines = %v, want none for 2 Books", b.hlDeadlines)
		}
	})

	t.Run("Notebook に到達しなかったら exit 0 で終わらない（種類を持たない失敗の exit 1）", func(t *testing.T) {
		setupXDG(t, "")
		b := &fakeBrowser{reached: false}
		stubBrowser(t, b, nil)

		code, stderr := runSync(t)
		if code != 1 {
			t.Errorf("run(sync) code = %d, want 1", code)
		}
		if want := "general error: sync: kindle notebook not reached\n"; stderr != want {
			t.Errorf("run(sync) stderr = %q, want %q", stderr, want)
		}
		if !b.closed {
			t.Error("browser not closed")
		}
		if b.listCalled != 0 {
			t.Errorf("ListBooks() called %d times, want 0", b.listCalled)
		}
	})

	t.Run("Notebook を開いた直後に Amazon の認証を確かめる", func(t *testing.T) {
		tests := []struct {
			name       string
			reached    bool
			pageURL    string
			pageHTML   []byte
			pageErr    error
			wantCode   int
			wantStderr string
			wantCalls  []string
			wantList   int
		}{
			{
				name:       "認証ページに送られたら exit 2",
				pageURL:    "https://www.amazon.co.jp/ap/signin?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook#secret-fragment",
				pageHTML:   []byte("<p>sign in</p>"),
				wantCode:   2,
				wantStderr: "Amazon authentication required: sync: redirected to the Amazon sign-in page \"www.amazon.co.jp/ap/signin\"; run `kindle-highlight-sync login` to sign in again\n",
				wantCalls:  []string{"OpenNotebook", "Page", "Close"},
				wantList:   0,
			},
			{
				name:       "Notebook の URL に login form があれば exit 2",
				reached:    true,
				pageURL:    "https://read.amazon.co.jp/notebook",
				pageHTML:   []byte(`<form name="signIn"><input name="email"></form>`),
				wantCode:   2,
				wantStderr: "Amazon authentication required: sync: a login form is present at \"read.amazon.co.jp/notebook\" without the Kindle Notebook library; run `kindle-highlight-sync login` to sign in again\n",
				wantCalls:  []string{"OpenNotebook", "Page", "Close"},
				wantList:   0,
			},
			{
				name:       "認証済みなら exit 0",
				reached:    true,
				pageURL:    "https://read.amazon.co.jp/notebook",
				pageHTML:   []byte(`<div id="kp-notebook-library"></div>`),
				wantCode:   0,
				wantStderr: "",
				wantList:   1,
			},
			{
				name:       "判定不能でも認証切れにせず exit 0",
				reached:    true,
				pageURL:    "https://read.amazon.co.jp/notebook",
				pageHTML:   []byte("<p>notebook</p>"),
				wantCode:   0,
				wantStderr: "",
				wantList:   1,
			},
			{
				name:       "判定不能で届いていなければ従来どおり exit 1",
				pageURL:    "https://example.com/other",
				pageHTML:   []byte("<p>other</p>"),
				wantCode:   1,
				wantStderr: "general error: sync: kindle notebook not reached\n",
				wantList:   0,
			},
			{
				name:       "Page の失敗はそのまま返す",
				reached:    true,
				pageErr:    errors.New("source: read page: boom"),
				wantCode:   1,
				wantStderr: "general error: source: read page: boom\n",
				wantList:   0,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupSyncedXDG(t, "")
				b := &fakeBrowser{
					reached:  tt.reached,
					pageURL:  tt.pageURL,
					pageHTML: tt.pageHTML,
					pageErr:  tt.pageErr,
				}
				stubBrowser(t, b, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				if tt.wantCalls != nil && !slices.Equal(b.calls, tt.wantCalls) {
					t.Errorf("calls = %v, want %v", b.calls, tt.wantCalls)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
				if b.pageCalled != 1 {
					t.Errorf("Page() called %d times, want 1", b.pageCalled)
				}
				if b.listCalled != tt.wantList {
					t.Errorf("ListBooks() called %d times, want %d", b.listCalled, tt.wantList)
				}
			})
		}
	})

	t.Run("Notebook を開くのに失敗したらその失敗をそのまま返す", func(t *testing.T) {
		setupXDG(t, "")
		b := &fakeBrowser{openErr: errors.New("source: open notebook: boom")}
		stubBrowser(t, b, nil)

		code, stderr := runSync(t)
		if code != 1 {
			t.Errorf("run(sync) code = %d, want 1", code)
		}
		if want := "general error: source: open notebook: boom\n"; stderr != want {
			t.Errorf("run(sync) stderr = %q, want %q", stderr, want)
		}
		if !b.closed {
			t.Error("browser not closed")
		}
		if b.listCalled != 0 {
			t.Errorf("ListBooks() called %d times, want 0", b.listCalled)
		}
	})

	t.Run("Chrome を起動できないと exit 5 で終わる", func(t *testing.T) {
		setupXDG(t, "")
		openErr := exitcode.New(exitcode.Browser, errors.New("source: start browser: exec: \"google-chrome\": executable file not found"))
		stubBrowser(t, nil, openErr)

		code, stderr := runSync(t)
		if code != 5 {
			t.Errorf("run(sync) code = %d, want 5", code)
		}
		if want := "browser error: source: start browser: exec: \"google-chrome\": executable file not found\n"; stderr != want {
			t.Errorf("run(sync) stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("ブラウザを閉じる失敗は、本体が成功したときだけ返す", func(t *testing.T) {
		closeErr := exitcode.New(exitcode.Browser, errors.New("source: close browser: boom"))
		tests := []struct {
			name       string
			reached    bool
			listErr    error
			hlErr      error
			wantCode   int
			wantStderr string
		}{
			{name: "到達して閉じるのに失敗", reached: true, wantCode: 5, wantStderr: "browser error: source: close browser: boom\n"},
			{name: "未到達を優先する", reached: false, wantCode: 1, wantStderr: "general error: sync: kindle notebook not reached\n"},
			{name: "取得の失敗を優先する", reached: true, listErr: errors.New("source: list books: page 1: boom"), wantCode: 1, wantStderr: "general error: source: list books: page 1: boom\n"},
			{name: "Highlight の取得の失敗を優先する", reached: true, hlErr: errors.New("source: list highlights B000000001: page 1: boom"), wantCode: 1, wantStderr: "general error: source: list highlights B000000001: page 1: boom\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupSyncedXDG(t, "")
				stubBrowser(t, &fakeBrowser{
					reached:      tt.reached,
					listErr:      tt.listErr,
					books:        []domain.Book{{ID: "book-id-1", ASIN: "B000000001"}},
					highlightErr: map[string]error{"book-id-1": tt.hlErr},
					closeErr:     closeErr,
				}, nil)

				code, stderr := runSync(t)
				if code != tt.wantCode {
					t.Errorf("run(sync) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, tt.wantStderr)
				}
			})
		}
	})

	t.Run("ブラウザは排他を解放する前に閉じられる", func(t *testing.T) {
		p := setupSyncedXDG(t, "")
		lockFile := filepath.Join(p.DataDir, "run.lock")
		codeAtClose := -1
		b, _ := stubReachedBrowser(t)
		b.onClose = func() {
			l, err := lock.Acquire(lockFile)
			codeAtClose = exitcode.Code(err)
			if err == nil {
				_ = l.Release()
			}
		}

		if code, stderr := runSync(t); code != 0 {
			t.Fatalf("run(sync) code = %d, stderr = %q, want 0", code, stderr)
		}
		if codeAtClose != 9 {
			t.Errorf("lock.Acquire during Close returned code %d, want 9 (lock still held)", codeAtClose)
		}
	})

	t.Run("先発が排他を持っている間の 2 つ目は待たずに exit 9 で終わる", func(t *testing.T) {
		for _, holder := range []string{"sync", "login"} {
			t.Run(holder+" が持っている", func(t *testing.T) {
				setupXDG(t, "")
				_, s := stubReachedBrowser(t)
				release, done := holdLock(t, holder)

				code, stderr := runSync(t)
				if code != 9 {
					t.Errorf("second run(sync) code = %d, want 9", code)
				}
				if stderr != wantAlreadyRunningErr {
					t.Errorf("second run(sync) stderr = %q, want %q", stderr, wantAlreadyRunningErr)
				}
				if s.opened != 0 {
					t.Errorf("openBrowser called %d times while %s holds the lock, want 0", s.opened, holder)
				}

				close(release)
				if got := <-done; got.code != 0 || got.stderr != "" {
					t.Errorf("first run(%s) code = %d, stderr = %q, want 0 and empty", holder, got.code, got.stderr)
				}
			})
		}
	})

	t.Run("前の実行の run.lock が残っていても誰も持っていなければ exit 0 で終わる", func(t *testing.T) {
		p := setupSyncedXDG(t, "")
		stubReachedBrowser(t)
		lockFile := filepath.Join(p.DataDir, "run.lock")
		if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockFile, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		code, stderr := runSync(t)
		assertSyncSucceeded(t, code, stderr, lockFile)
	})

	t.Run("壊れた config.toml なら前処理で exit 3 で止まる", func(t *testing.T) {
		p := setupXDG(t, invalidTOML)
		_, s := stubReachedBrowser(t)
		code, stderr := runSync(t)
		if code != 3 {
			t.Errorf("run(sync) code = %d, want 3", code)
		}
		wantStderr := "configuration / confirmation required: " + p.ConfigFile + ": invalid TOML (line 1, column 8)\n"
		if stderr != wantStderr {
			t.Errorf("run(sync) stderr = %q, want %q", stderr, wantStderr)
		}
		if s.opened != 0 {
			t.Errorf("openBrowser called %d times, want 0", s.opened)
		}
	})

	t.Run("--help の一覧に sync が出る", func(t *testing.T) {
		setupXDG(t, "")
		if !helpLists(t, "sync", wantSyncShort) {
			t.Error("--help output does not list sync")
		}
	})

	t.Run("sync --help に各フラグとその説明が出る", func(t *testing.T) {
		setupXDG(t, "")
		var stdout, stderr bytes.Buffer
		if code := run([]string{"sync", "--help"}, &stdout, &stderr); code != 0 {
			t.Errorf("run(sync --help) code = %d, stderr = %q, want 0", code, stderr.String())
		}
		for flag, usage := range map[string]string{"--headed": wantSyncHeadedUsage, "--full": wantSyncFullUsage, "--dry-run": wantSyncDryRunUsage, "--yes": wantSyncYesUsage} {
			var found bool
			for line := range strings.Lines(stdout.String()) {
				if strings.Contains(line, flag) && strings.Contains(line, usage) {
					found = true
				}
			}
			if !found {
				t.Errorf("sync --help output does not list %s: %q", flag, stdout.String())
			}
		}
	})
}
