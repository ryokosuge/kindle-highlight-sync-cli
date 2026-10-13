package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

// applyAdoptPath は apply の fixture で adoptable になる Book のページを更新する path。
const applyAdoptPath = testPagesPath + "/page-adopt"

// applyExactPath は apply の fixture で exact になる Book のページを更新する path。
const applyExactPath = testPagesPath + "/page-exact"

// applyReplies は initialReplies に、作成・Adopt の応答を足した Notion の応答。
func applyReplies() map[string]string {
	return initialReplies()
}

// runSyncApply は sync を args 付きで走らせ、標準出力と標準エラーを返す。
func runSyncApply(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var stdoutBuf, stderrBuf bytes.Buffer
	code = run(append([]string{"sync"}, args...), &stdoutBuf, &stderrBuf)
	return code, stdoutBuf.String(), stderrBuf.String()
}

// writeRequests は requests のうち Notion を変える要求（POST /v1/pages と PATCH /v1/pages/...）だけを "METHOD path" で返す。
func writeRequests(rs []recordedRequest) []string {
	var lines []string
	for _, r := range rs {
		if strings.HasPrefix(r.path, testPagesPath) {
			lines = append(lines, r.method+" "+r.path)
		}
	}
	return lines
}

// appendRequests は requests のうち Highlight の追記の要求（PATCH /v1/blocks/{page_id}/children）だけを "METHOD path" で返す。
func appendRequests(rs []recordedRequest) []string {
	var lines []string
	for _, r := range rs {
		if r.method == http.MethodPatch && strings.HasPrefix(r.path, "/v1/blocks/") {
			lines = append(lines, r.method+" "+r.path)
		}
	}
	return lines
}

func TestSyncApply(t *testing.T) {
	t.Run("--yes の Initial Sync は none の Book を作成し、adoptable の Book を Adopt して、件数だけを標準出力に出す", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, applyReplies())
		stubSyncPrompter(t, "", false)
		b := newInitialBrowser()
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t, "--yes")
		if code != 0 || stderr != "" {
			t.Errorf("run(sync --yes) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("1", "1", "3", "0", "0"), "3", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		for _, secret := range []string{"Distinctive", testDryRunToken} {
			if strings.Contains(stdout+stderr, secret) {
				t.Errorf("output contains %q: stdout = %q, stderr = %q", secret, stdout, stderr)
			}
		}
		// Highlight を持つ exact・作成・Adopt した Book のページには、既知を除いた分を追記する
		if got, want := appendRequests(requests()), []string{"PATCH " + testDryRunBlocksPath, "PATCH " + testCreatedBlocksPath, "PATCH " + testInitialAdoptBlocksPath}; !slices.Equal(got, want) {
			t.Errorf("append requests = %q, want %q", got, want)
		}
		// Book ごとに 1 回ずつ
		if got, want := writeRequests(requests()), []string{"PATCH " + applyExactPath, "POST " + testPagesPath, "PATCH " + applyAdoptPath}; !slices.Equal(got, want) {
			t.Errorf("write requests = %q, want %q", got, want)
		}
		if !initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at not recorded")
		}
	})

	t.Run("ambiguous の Book には書き込みの要求を出さず、Book ID を標準エラーに出して exit 8 で終わり、partial として記録する", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, applyReplies())
		stubSyncPrompter(t, "", false)
		b := newInitialBrowser()
		b.books = initialBooksWithAmbiguous
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t, "--yes")
		if want := "partial failure: sync: 1 books were not synced: " + dryRunBookAmbiguous.ID + " (ambiguous)\n"; code != 8 || stderr != want {
			t.Errorf("run(sync --yes) code = %d, stderr = %q, want 8 and %q", code, stderr, want)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("1", "1", "3", "1", "0"), "4", "4"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		// 書き込みは none と adoptable の Book の 2 回だけで、ambiguous の Book の分は無い
		if got, want := writeRequests(requests()), []string{"PATCH " + applyExactPath, "POST " + testPagesPath, "PATCH " + applyAdoptPath}; !slices.Equal(got, want) {
			t.Errorf("write requests = %q, want %q", got, want)
		}
		if !initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at not recorded after exit 8")
		}
	})

	t.Run("作成・Adopt したページを返す Notion で続けて sync すると、それらの Book は exact と照合されて作成・Adopt はせず、UpdateBook だけを呼ぶ", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		replies := applyReplies()
		stubSyncPrompter(t, "", false)
		requests, _ := setupDryRunNotion(t, replies)
		stubBrowser(t, newInitialBrowser(), nil)
		if code, _, stderr := runSyncApply(t, "--yes"); code != 0 {
			t.Fatalf("1st run(sync --yes) code = %d, stderr = %q, want 0", code, stderr)
		}
		firstWrites := len(writeRequests(requests()))
		firstAppends := len(requests())

		// 作成・Adopt 後の Data Source を返す。続く sync はこれらの Book を exact と照合する
		replies[testDryRunQueryPath] = queryBody(
			dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
			dryRunPageJSON("page-created", dryRunBookNew.ID, dryRunBookNew.Title),
			dryRunPageJSON("page-adopt", initialBookAdopt.ID, initialBookAdopt.Title),
		)
		replies[testPagesPath+"/page-created"] = `{"object":"page","id":"page-created"}`
		b2 := newInitialBrowser()
		stubBrowser(t, b2, nil)
		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("2nd run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("0", "0", "0", "0", "0"), "3", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("2nd stdout cells = %q, want %q", got, want)
		}
		// POST /v1/pages は届かず、exact になった 3 冊のページに PATCH が 1 回ずつ届く
		want := []string{"PATCH " + applyExactPath, "PATCH " + testPagesPath + "/page-created", "PATCH " + applyAdoptPath}
		if got := writeRequests(requests())[firstWrites:]; !slices.Equal(got, want) {
			t.Errorf("write requests in 2nd run = %q, want %q", got, want)
		}
		// 追記した Highlight は既知として読まれ、追記の要求は届かない
		if got := appendRequests(requests()[firstAppends:]); got != nil {
			t.Errorf("append requests in 2nd run = %q, want none", got)
		}
		if !initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at not recorded")
		}
	})

	t.Run("追記した Highlight を返す Notion で続けて sync --dry-run すると、それらの Highlight を Append の予定に数えず、追記の要求も出さない", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		replies := applyReplies()
		requests, _ := setupDryRunNotion(t, replies)
		stubBrowser(t, newInitialBrowser(), nil)
		if code, _, stderr := runSyncApply(t); code != 0 {
			t.Fatalf("run(sync) code = %d, stderr = %q, want 0", code, stderr)
		}
		firstRequests := len(requests())

		replies[testDryRunQueryPath] = queryBody(
			dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
			dryRunPageJSON("page-created", dryRunBookNew.ID, dryRunBookNew.Title),
			dryRunPageJSON("page-adopt", initialBookAdopt.ID, initialBookAdopt.Title),
		)
		stubBrowser(t, newInitialBrowser(), nil)
		code, stdout, stderr := runSyncDryRun(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync --dry-run) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if !strings.Contains(stdout, "Highlights to append  0\n") {
			t.Errorf("stdout = %q, want the summary to count 0 Highlights to append", stdout)
		}
		if got := appendRequests(requests()[firstRequests:]); got != nil {
			t.Errorf("append requests in dry-run = %q, want none", got)
		}
	})

	t.Run("追記が失敗した Book があれば Book ID を標準エラーに出して exit 8 で終わり、Highlight の本文・Note・token は出さない", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		keyring.MockInit()
		t.Setenv(secrets.EnvNotionToken, testDryRunToken)
		reply := dryRunNotionReply(t, applyReplies())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch && r.URL.EscapedPath() == testCreatedBlocksPath {
				w.WriteHeader(http.StatusBadRequest) // 作成したページへの追記が 400（Book 単位の失敗）
				_, _ = w.Write([]byte(`{"object":"error","status":400,"code":"validation_error","message":"Distinctive Highlight Three"}`))
				return
			}
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write([]byte(reply(r.Method, r.URL.EscapedPath(), body)))
		}))
		t.Cleanup(srv.Close)
		stubNewNotionClient(t, srv)
		stubBrowser(t, newInitialBrowser(), nil)

		code, stdout, stderr := runSyncApply(t)
		if want := "partial failure: sync: 1 books were not synced: " + dryRunBookNew.ID + " (notion write failed)\n"; code != 8 || stderr != want {
			t.Errorf("run(sync) code = %d, stderr = %q, want 8 and %q", code, stderr, want)
		}
		// exact の 1 件は追記できている
		if got, want := planCells(t, stdout), withTargetCount(applyCells("1", "1", "2", "0", "1"), "3", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		for _, secret := range []string{"Distinctive", testDryRunToken} {
			if strings.Contains(stdout+stderr, secret) {
				t.Errorf("output contains %q: stdout = %q, stderr = %q", secret, stdout, stderr)
			}
		}
	})

	t.Run("Initial Sync 済みの sync でも、新しく増えた none / adoptable の Book を作成・Adopt する", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, applyReplies())
		stubSyncPrompter(t, "", false) // 問いが出れば入力が無く止まる
		b := newInitialBrowser()
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("1", "1", "3", "0", "0"), "3", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		// 予定の算出も確認も行わず、照合の query のあとに書き込む
		assertRequests(t, requests(), initialApplyRequests...)
	})

	t.Run("ASIN の無い Book は書き込まず、ambiguous の skip と重なっても exit 8 を返す", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, applyReplies())
		b := newInitialBrowser()
		b.books = slices.Concat(initialBooksWithAmbiguous, []domain.Book{dryRunBookNoASIN})
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t)
		if want := "partial failure: sync: 2 books were not synced: B000000003 (ambiguous); " + dryRunBookNoASIN.ID + " (book has no ASIN)\n"; code != 8 || stderr != want {
			t.Errorf("run(sync) code = %d, stderr = %q, want 8 and %q", code, stderr, want)
		}
		if got, want := planCells(t, stdout), withTargetCount(applyCells("1", "1", "3", "1", "1"), "5", "5"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		if got, want := writeRequests(requests()), []string{"PATCH " + applyExactPath, "POST " + testPagesPath, "PATCH " + applyAdoptPath}; !slices.Equal(got, want) {
			t.Errorf("write requests = %q, want %q", got, want)
		}
	})

	t.Run("確認を拒否した run は書き込まず、記録もしない", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, applyReplies())
		stubSyncPrompter(t, "N\n", true)
		stubBrowser(t, newInitialBrowser(), nil)

		code, stdout, _ := runSyncApply(t)
		if code != 3 || stdout != "" {
			t.Errorf("run(sync) code = %d, stdout = %q, want 3 and empty", code, stdout)
		}
		if got := writeRequests(requests()); got != nil {
			t.Errorf("write requests = %q, want none", got)
		}
		if got := appendRequests(requests()); got != nil {
			t.Errorf("append requests = %q, want none", got)
		}
		if initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at recorded after decline")
		}
	})

	t.Run("書き込みが fatal な失敗で止まっても、そこまでに書いた分の件数を出してから exit 4 で終わる", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		keyring.MockInit()
		t.Setenv(secrets.EnvNotionToken, testDryRunToken)
		reply := dryRunNotionReply(t, applyReplies())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch && r.URL.EscapedPath() == applyAdoptPath {
				w.WriteHeader(http.StatusNotFound) // Adopt するページに届かない（fatal）
				_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found","message":"missing"}`))
				return
			}
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write([]byte(reply(r.Method, r.URL.EscapedPath(), body)))
		}))
		t.Cleanup(srv.Close)
		stubNewNotionClient(t, srv)
		stubBrowser(t, newInitialBrowser(), nil)

		code, stdout, stderr := runSyncApply(t)
		if code != 4 || !strings.HasPrefix(stderr, "Notion error: ") {
			t.Errorf("run(sync) code = %d, stderr = %q, want 4 with a Notion error", code, stderr)
		}
		// none の Book の作成までは済んでいる
		if got, want := planCells(t, stdout), withTargetCount(applyCells("1", "0", "2", "0", "0"), "3", "3"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		for _, secret := range []string{"Distinctive", testDryRunToken} {
			if strings.Contains(stdout+stderr, secret) {
				t.Errorf("output contains %q: stdout = %q, stderr = %q", secret, stdout, stderr)
			}
		}
	})
}

func TestSyncApplyMixedBookFailures(t *testing.T) {
	bookIDless := domain.Book{ID: "B000000009", ASIN: "B000000009", Title: "Distinctive Book Idless"}
	structureErr := fmt.Errorf("source: list highlights %s: page 1: row 1: %w", dryRunBookExact.ID,
		exitcode.New(exitcode.SourceFormat, fmt.Errorf("highlight id is missing: %w", source.ErrSourceFormat)))
	idlessErr := fmt.Errorf("source: list highlights %s: page 1: row 1: %w: %w", bookIDless.ID, domain.ErrMissingAnnotationID,
		exitcode.New(exitcode.SourceFormat, source.ErrSourceFormat))

	tests := []struct {
		name         string
		args         []string
		initial      bool
		wantFailures string // Book ID と種類の並び（skip → 失敗の順）
	}{
		{
			name: "Initial Sync 済みの sync は Book 一覧の順に失敗を並べる",
			wantFailures: "5 books were not synced: " + dryRunBookAmbiguous.ID + " (ambiguous); " +
				dryRunBookExact.ID + " (unexpected notebook structure); " + dryRunBookNew.ID + " (notion write failed); " +
				dryRunBookNoASIN.ID + " (book has no ASIN); " + bookIDless.ID + " (highlight ID cannot be made)",
		},
		{
			// 予定の算出で失敗に残った Book は実行に渡さず、実行の失敗の前に並べる
			name:    "Initial Sync は確認の後に残りの Book を同期し、partial として記録する",
			args:    []string{"--yes"},
			initial: true,
			wantFailures: "5 books were not synced: " + dryRunBookAmbiguous.ID + " (ambiguous); " +
				dryRunBookExact.ID + " (unexpected notebook structure); " + dryRunBookNoASIN.ID + " (book has no ASIN); " +
				bookIDless.ID + " (highlight ID cannot be made); " + dryRunBookNew.ID + " (notion write failed)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setup := setupSyncedXDG
			if tt.initial {
				setup = setupXDG
			}
			p := setup(t, validConfigTOML)
			keyring.MockInit()
			t.Setenv(secrets.EnvNotionToken, testDryRunToken)
			reply := dryRunNotionReply(t, applyReplies())
			var requests []recordedRequest
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, recordedRequest{method: r.Method, path: r.URL.EscapedPath()})
				mu.Unlock()
				if r.Method == http.MethodPatch && r.URL.EscapedPath() == testCreatedBlocksPath {
					w.WriteHeader(http.StatusInternalServerError) // 作成したページへの追記が 5xx（Book 単位の失敗）
					_, _ = w.Write([]byte(`{"object":"error","status":500,"code":"internal_server_error","message":"Distinctive Highlight Three"}`))
					return
				}
				_, _ = w.Write([]byte(reply(r.Method, r.URL.EscapedPath(), body)))
			}))
			t.Cleanup(srv.Close)
			stubNewNotionClient(t, srv)
			stubSyncPrompter(t, "", false)
			b := newInitialBrowser()
			// exact は構造の異常、new は書き込みの失敗、adopt は成功、ambiguous は skip、ASIN 無しと Highlight ID が作れない Book も混ぜる
			b.books = []domain.Book{dryRunBookExact, dryRunBookNew, initialBookAdopt, dryRunBookAmbiguous, dryRunBookNoASIN, bookIDless}
			b.highlightErr = map[string]error{dryRunBookExact.ID: structureErr, bookIDless.ID: idlessErr}
			stubBrowser(t, b, nil)

			code, stdout, stderr := runSyncApply(t, tt.args...)
			want := "partial failure: sync: " + tt.wantFailures + "\n"
			if code != 8 || !strings.HasSuffix(stderr, want) {
				t.Errorf("run(sync) code = %d, stderr = %q, want 8 and suffix %q", code, stderr, want)
			}
			// 失敗した Book があっても残りの Adopt した Book のページに追記の要求が届く
			if !slices.Contains(appendRequests(requests), "PATCH "+testInitialAdoptBlocksPath) {
				t.Errorf("append requests = %q, want to contain PATCH %s", appendRequests(requests), testInitialAdoptBlocksPath)
			}
			for _, secret := range []string{"Distinctive", testDryRunToken} {
				if strings.Contains(stdout+stderr, secret) {
					t.Errorf("output contains %q: stdout = %q, stderr = %q", secret, stdout, stderr)
				}
			}
			if tt.initial && !initialSyncRecorded(t, p) {
				t.Error("initial_sync_completed_at not recorded, want recorded as partial")
			}
		})
	}
}
