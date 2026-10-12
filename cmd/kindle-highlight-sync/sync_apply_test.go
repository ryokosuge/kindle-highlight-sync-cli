package main

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// applyAdoptPath は apply の fixture で adoptable になる Book のページを更新する path。
const applyAdoptPath = testPagesPath + "/page-adopt"

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
		if got, want := planCells(t, stdout), applyCells("1", "1", "0", "0"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		for _, secret := range []string{"Distinctive", testDryRunToken} {
			if strings.Contains(stdout+stderr, secret) {
				t.Errorf("output contains %q: stdout = %q, stderr = %q", secret, stdout, stderr)
			}
		}
		// Book ごとに 1 回ずつ
		if got, want := writeRequests(requests()), []string{"POST " + testPagesPath, "PATCH " + applyAdoptPath}; !slices.Equal(got, want) {
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
		if got, want := planCells(t, stdout), applyCells("1", "1", "1", "0"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		// 書き込みは none と adoptable の Book の 2 回だけで、ambiguous の Book の分は無い
		if got, want := writeRequests(requests()), []string{"POST " + testPagesPath, "PATCH " + applyAdoptPath}; !slices.Equal(got, want) {
			t.Errorf("write requests = %q, want %q", got, want)
		}
		if !initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at not recorded after exit 8")
		}
	})

	t.Run("作成・Adopt したページを返す Notion で続けて sync すると、それらの Book は exact と照合されて書き込まない", func(t *testing.T) {
		p := setupXDG(t, validConfigTOML)
		replies := applyReplies()
		stubSyncPrompter(t, "", false)
		requests, _ := setupDryRunNotion(t, replies)
		stubBrowser(t, newInitialBrowser(), nil)
		if code, _, stderr := runSyncApply(t, "--yes"); code != 0 {
			t.Fatalf("1st run(sync --yes) code = %d, stderr = %q, want 0", code, stderr)
		}
		firstWrites := writeRequests(requests())

		// 作成・Adopt 後の Data Source を返す。続く sync はこれらの Book を exact と照合する
		replies[testDryRunQueryPath] = queryBody(
			dryRunPageJSON("page-exact", dryRunBookExact.ID, dryRunBookExact.Title),
			dryRunPageJSON("page-created", dryRunBookNew.ID, dryRunBookNew.Title),
			dryRunPageJSON("page-adopt", initialBookAdopt.ID, initialBookAdopt.Title),
		)
		b2 := newInitialBrowser()
		stubBrowser(t, b2, nil)
		code, stdout, stderr := runSyncApply(t)
		if code != 0 || stderr != "" {
			t.Errorf("2nd run(sync) code = %d, stderr = %q, want 0 and empty", code, stderr)
		}
		if got, want := planCells(t, stdout), applyCells("0", "0", "0", "0"); !reflect.DeepEqual(got, want) {
			t.Errorf("2nd stdout cells = %q, want %q", got, want)
		}
		if got := writeRequests(requests()); !slices.Equal(got, firstWrites) {
			t.Errorf("write requests after 2nd run = %q, want unchanged %q", got, firstWrites)
		}
		if !initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at not recorded")
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
		if got, want := planCells(t, stdout), applyCells("1", "1", "0", "0"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		// 予定の算出も確認も行わず、照合の query のあとに書き込む
		assertRequests(t, requests(), "POST "+testDryRunQueryPath, "POST "+testPagesPath, "PATCH "+applyAdoptPath)
	})

	t.Run("ASIN の無い Book は書き込まず、ambiguous の skip と重なっても exit 7 を返す", func(t *testing.T) {
		setupSyncedXDG(t, validConfigTOML)
		requests, _ := setupDryRunNotion(t, applyReplies())
		b := newInitialBrowser()
		b.books = slices.Concat(initialBooksWithAmbiguous, []domain.Book{dryRunBookNoASIN})
		stubBrowser(t, b, nil)

		code, stdout, stderr := runSyncApply(t)
		if want := "source format error: sync: highlights of 1 of 5 books could not be listed: " + dryRunBookNoASIN.ID + " (book has no ASIN)\n"; code != 7 || stderr != want {
			t.Errorf("run(sync) code = %d, stderr = %q, want 7 and %q", code, stderr, want)
		}
		if got, want := planCells(t, stdout), applyCells("1", "1", "1", "0"); !reflect.DeepEqual(got, want) {
			t.Errorf("stdout cells = %q, want %q", got, want)
		}
		if got, want := writeRequests(requests()), []string{"POST " + testPagesPath, "PATCH " + applyAdoptPath}; !slices.Equal(got, want) {
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
		if initialSyncRecorded(t, p) {
			t.Error("initial_sync_completed_at recorded after decline")
		}
	})
}
