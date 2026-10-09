package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	wantLoginShort  = "画面付きの Chrome で Amazon にログインし、sync が使うセッションを保存する"
	wantLoginStdout = "ブラウザで Amazon にログインしてください\n"
)

// errNotReached は WaitNotebook が ctx の打ち切りで返す形（internal/source の notReached と同じ）。
func errNotReached(cause error) error {
	return fmt.Errorf("source: %w (last URL %q): %w", source.ErrNotebookNotReached, "www.amazon.co.jp/ap/signin", cause)
}

func runLogin(t *testing.T) (code int, stdout, stderr string) {
	t.Helper()
	var stdoutBuf, stderrBuf bytes.Buffer
	code = run([]string{"login"}, &stdoutBuf, &stderrBuf)
	return code, stdoutBuf.String(), stderrBuf.String()
}

type runResult struct {
	code   int
	stderr string
}

// holdLock は cmd（login か sync）の本体を止めたまま goroutine で起動し、排他を取るまで待つ。
// release を close すると本体が戻り、結果が done に届く。
func holdLock(t *testing.T, cmd string) (release chan<- struct{}, done <-chan runResult) {
	t.Helper()
	acquired, rel := make(chan struct{}), make(chan struct{})
	body := func() error {
		close(acquired)
		<-rel
		return nil
	}
	switch cmd {
	case "login":
		orig := loginBody
		loginBody = func(context.Context, *app, io.Writer) error { return body() }
		t.Cleanup(func() { loginBody = orig })
	case "sync":
		orig := syncBody
		syncBody = func(context.Context, *app) error { return body() }
		t.Cleanup(func() { syncBody = orig })
	default:
		t.Fatalf("holdLock: unknown cmd %q", cmd)
	}

	first := make(chan runResult, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := run([]string{cmd}, &stdout, &stderr)
		first <- runResult{code: code, stderr: stderr.String()}
	}()
	select {
	case <-acquired:
	case got := <-first:
		t.Fatalf("first run(%s) returned before its body ran: code = %d, stderr = %q", cmd, got.code, got.stderr)
	}
	return rel, first
}

// helpLists は --help の一覧に name と short が並んで出るかを返す。
func helpLists(t *testing.T, name, short string) bool {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(--help) code = %d, want 0", code)
	}
	for line := range strings.Lines(stdout.String()) {
		if strings.HasPrefix(line, "  "+name+" ") && strings.Contains(line, short) {
			return true
		}
	}
	t.Logf("--help output: %q", stdout.String())
	return false
}

func TestLogin(t *testing.T) {
	t.Run("排他を取り browser-profile を headed で開き、Notebook に到達すると exit 0 で終わる", func(t *testing.T) {
		p := setupXDG(t, "")
		b := &fakeBrowser{}
		s := stubBrowser(t, b, nil)

		code, stdout, stderr := runLogin(t)
		if code != 0 {
			t.Errorf("run(login) code = %d, want 0", code)
		}
		if stdout != wantLoginStdout {
			t.Errorf("run(login) stdout = %q, want %q", stdout, wantLoginStdout)
		}
		if stderr != "" {
			t.Errorf("run(login) stderr = %q, want empty", stderr)
		}
		wantOpts := source.Options{ProfileDir: filepath.Join(p.DataDir, "browser-profile"), Headless: false}
		if s.opened != 1 || s.opts != wantOpts {
			t.Errorf("openBrowser called %d times with %+v, want once with %+v", s.opened, s.opts, wantOpts)
		}
		if !b.closed {
			t.Error("browser not closed")
		}
		if _, err := os.Stat(filepath.Join(p.DataDir, "run.lock")); err != nil {
			t.Errorf("Stat(run.lock) error = %v, want run.lock to exist", err)
		}
	})

	t.Run("Notebook の待ちには 10 分の期限が付き、Open に渡す ctx には付かない", func(t *testing.T) {
		setupXDG(t, "")
		b := &fakeBrowser{}
		origOpen := openBrowser
		openBrowser = func(ctx context.Context, opts source.Options) (browser, error) {
			if d, ok := ctx.Deadline(); ok {
				t.Errorf("openBrowser ctx has deadline %v, want none", d)
			}
			return origOpen(ctx, opts)
		}
		t.Cleanup(func() { openBrowser = origOpen })
		stubBrowser(t, b, nil)

		if code, _, stderr := runLogin(t); code != 0 {
			t.Fatalf("run(login) code = %d, stderr = %q, want 0", code, stderr)
		}
		if b.waitDeadline.IsZero() {
			t.Fatal("WaitNotebook ctx has no deadline, want 10m")
		}
		if wait := b.waitDeadline.Sub(b.waitCalledAt); wait > 10*time.Minute || wait < 9*time.Minute {
			t.Errorf("WaitNotebook deadline is %v ahead, want 10m", wait)
		}
	})

	t.Run("Notebook に到達しないまま待ちが切れるかブラウザが閉じられると exit 2 で終わる", func(t *testing.T) {
		tests := []struct {
			name       string
			waitErr    error
			wantStderr string
		}{
			{
				name:       "待ちの打ち切り",
				waitErr:    errNotReached(context.DeadlineExceeded),
				wantStderr: "Amazon authentication required: source: kindle notebook not reached (last URL \"www.amazon.co.jp/ap/signin\"): context deadline exceeded\n",
			},
			{
				name:       "本人がブラウザを閉じた",
				waitErr:    errNotReached(errors.New("browser closed")),
				wantStderr: "Amazon authentication required: source: kindle notebook not reached (last URL \"www.amazon.co.jp/ap/signin\"): browser closed\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupXDG(t, "")
				b := &fakeBrowser{waitErr: tt.waitErr}
				stubBrowser(t, b, nil)

				code, stdout, stderr := runLogin(t)
				if code != 2 {
					t.Errorf("run(login) code = %d, want 2", code)
				}
				if stdout != wantLoginStdout {
					t.Errorf("run(login) stdout = %q, want %q", stdout, wantLoginStdout)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(login) stderr = %q, want %q", stderr, tt.wantStderr)
				}
				if !b.closed {
					t.Error("browser not closed")
				}
			})
		}
	})

	t.Run("Notebook の待ちがそれ以外で失敗したら種類を付けずに返す", func(t *testing.T) {
		setupXDG(t, "")
		b := &fakeBrowser{waitErr: errors.New("source: open notebook: boom")}
		stubBrowser(t, b, nil)

		code, _, stderr := runLogin(t)
		if code != 1 {
			t.Errorf("run(login) code = %d, want 1", code)
		}
		if want := "general error: source: open notebook: boom\n"; stderr != want {
			t.Errorf("run(login) stderr = %q, want %q", stderr, want)
		}
		if !b.closed {
			t.Error("browser not closed")
		}
	})

	t.Run("Chrome を起動できないと exit 5 で終わる", func(t *testing.T) {
		setupXDG(t, "")
		openErr := exitcode.New(exitcode.Browser, errors.New("source: start browser: exec: \"google-chrome\": executable file not found"))
		stubBrowser(t, nil, openErr)

		code, stdout, stderr := runLogin(t)
		if code != 5 {
			t.Errorf("run(login) code = %d, want 5", code)
		}
		if stdout != "" {
			t.Errorf("run(login) stdout = %q, want empty", stdout)
		}
		if want := "browser error: source: start browser: exec: \"google-chrome\": executable file not found\n"; stderr != want {
			t.Errorf("run(login) stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("ブラウザを閉じる失敗は、待ちが成功したときだけ返す", func(t *testing.T) {
		closeErr := exitcode.New(exitcode.Browser, errors.New("source: close browser: boom"))
		tests := []struct {
			name       string
			waitErr    error
			wantCode   int
			wantStderr string
		}{
			{name: "到達して閉じるのに失敗", wantCode: 5, wantStderr: "browser error: source: close browser: boom\n"},
			{
				name:       "待ちの失敗を優先する",
				waitErr:    errNotReached(context.DeadlineExceeded),
				wantCode:   2,
				wantStderr: "Amazon authentication required: source: kindle notebook not reached (last URL \"www.amazon.co.jp/ap/signin\"): context deadline exceeded\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				setupXDG(t, "")
				stubBrowser(t, &fakeBrowser{waitErr: tt.waitErr, closeErr: closeErr}, nil)

				code, _, stderr := runLogin(t)
				if code != tt.wantCode {
					t.Errorf("run(login) code = %d, want %d", code, tt.wantCode)
				}
				if stderr != tt.wantStderr {
					t.Errorf("run(login) stderr = %q, want %q", stderr, tt.wantStderr)
				}
			})
		}
	})

	t.Run("別の login / sync が排他を持っている間は待たずに exit 9 で終わる", func(t *testing.T) {
		for _, holder := range []string{"login", "sync"} {
			t.Run(holder+" が持っている", func(t *testing.T) {
				setupXDG(t, "")
				s := stubBrowser(t, &fakeBrowser{}, nil)
				release, done := holdLock(t, holder)

				code, stdout, stderr := runLogin(t)
				if code != 9 {
					t.Errorf("second run(login) code = %d, want 9", code)
				}
				if stdout != "" {
					t.Errorf("second run(login) stdout = %q, want empty", stdout)
				}
				if stderr != wantAlreadyRunningErr {
					t.Errorf("second run(login) stderr = %q, want %q", stderr, wantAlreadyRunningErr)
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

	t.Run("ブラウザは排他を解放する前に閉じられる", func(t *testing.T) {
		p := setupXDG(t, "")
		lockFile := filepath.Join(p.DataDir, "run.lock")
		codeAtClose := -1
		b := &fakeBrowser{}
		b.onClose = func() {
			l, err := lock.Acquire(lockFile)
			codeAtClose = exitcode.Code(err)
			if err == nil {
				_ = l.Release()
			}
		}
		stubBrowser(t, b, nil)

		if code, _, stderr := runLogin(t); code != 0 {
			t.Fatalf("run(login) code = %d, stderr = %q, want 0", code, stderr)
		}
		if codeAtClose != 9 {
			t.Errorf("lock.Acquire during Close returned code %d, want 9 (lock still held)", codeAtClose)
		}
		if l, err := lock.Acquire(lockFile); err != nil {
			t.Errorf("lock.Acquire after login error = %v, want lock released", err)
		} else {
			_ = l.Release()
		}
	})

	t.Run("壊れた config.toml なら前処理で exit 3 で止まる", func(t *testing.T) {
		p := setupXDG(t, invalidTOML)
		s := stubBrowser(t, &fakeBrowser{}, nil)
		code, _, stderr := runLogin(t)
		if code != 3 {
			t.Errorf("run(login) code = %d, want 3", code)
		}
		wantStderr := "configuration / confirmation required: " + p.ConfigFile + ": invalid TOML (line 1, column 8)\n"
		if stderr != wantStderr {
			t.Errorf("run(login) stderr = %q, want %q", stderr, wantStderr)
		}
		if s.opened != 0 {
			t.Errorf("openBrowser called %d times, want 0", s.opened)
		}
	})

	t.Run("--help の一覧に login が出る", func(t *testing.T) {
		setupXDG(t, "")
		if !helpLists(t, "login", wantLoginShort) {
			t.Error("--help output does not list login")
		}
	})
}
