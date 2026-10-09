package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/lock"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

const (
	wantSyncShort         = "Kindle の Highlight と Note を Notion に同期する"
	wantSyncHeadedUsage   = "画面付きの Chrome で同期する（既定は headless）"
	wantAlreadyRunningErr = "already running: another kindle-highlight-sync process is running\n"
)

// stubReachedBrowser は Notebook に到達する fake に openBrowser を差し替える。CI に Chrome は無いので、sync を走らせるケースは必ず差し替える。
func stubReachedBrowser(t *testing.T) (*fakeBrowser, *browserStub) {
	t.Helper()
	b := &fakeBrowser{reached: true}
	return b, stubBrowser(t, b, nil)
}

func runSync(t *testing.T) (code int, stderr string) {
	t.Helper()
	var stdout, stderrBuf bytes.Buffer
	code = run([]string{"sync"}, &stdout, &stderrBuf)
	if stdout.Len() != 0 {
		t.Errorf("run(sync) stdout = %q, want empty", stdout.String())
	}
	return code, stderrBuf.String()
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
				p := setupXDG(t, "")
				b, s := stubReachedBrowser(t)

				var stdout, stderr bytes.Buffer
				code := run(tt.args, &stdout, &stderr)
				if stdout.Len() != 0 {
					t.Errorf("run(%v) stdout = %q, want empty", tt.args, stdout.String())
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

	t.Run("Notebook を開く action には 1 分の期限が付き、Open に渡す ctx には付かない", func(t *testing.T) {
		setupXDG(t, "")
		b := &fakeBrowser{reached: true}
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
			wantCode   int
			wantStderr string
		}{
			{name: "到達して閉じるのに失敗", reached: true, wantCode: 5, wantStderr: "browser error: source: close browser: boom\n"},
			{name: "未到達を優先する", reached: false, wantCode: 1, wantStderr: "general error: sync: kindle notebook not reached\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupXDG(t, "")
				stubBrowser(t, &fakeBrowser{reached: tt.reached, closeErr: closeErr}, nil)

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
		p := setupXDG(t, "")
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
		p := setupXDG(t, "")
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

	t.Run("sync --help に --headed とその説明が出る", func(t *testing.T) {
		setupXDG(t, "")
		var stdout, stderr bytes.Buffer
		if code := run([]string{"sync", "--help"}, &stdout, &stderr); code != 0 {
			t.Errorf("run(sync --help) code = %d, stderr = %q, want 0", code, stderr.String())
		}
		var found bool
		for line := range strings.Lines(stdout.String()) {
			if strings.Contains(line, "--headed") && strings.Contains(line, wantSyncHeadedUsage) {
				found = true
			}
		}
		if !found {
			t.Errorf("sync --help output does not list --headed: %q", stdout.String())
		}
	})
}
