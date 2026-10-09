package source

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// chromeEnv に Chrome の実行ファイルを入れたときだけ、実 Chrome を起動するテストを走らせる（CI には Chrome が無い）。
const chromeEnv = "KHS_TEST_CHROME"

func chromePath(t *testing.T) string {
	t.Helper()
	path := os.Getenv(chromeEnv)
	if path == "" {
		t.Skipf("%s が空なので実 Chrome を起動するテストを skip する", chromeEnv)
	}
	return path
}

type launch struct {
	path string
	args []string
}

// launchCmd は flags(opts) で Chrome を起動するときの実行ファイルと引数を返す。
// 起動の直前に実行ファイルを存在しないものに差し替えるので、Chrome は起動しない。
func launchCmd(t *testing.T, opts Options) launch {
	t.Helper()
	var got launch
	missing := filepath.Join(t.TempDir(), "no-such-chrome")
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(flags(opts), chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
			got = launch{path: cmd.Path, args: slices.Clone(cmd.Args[1:])}
			cmd.Path = missing
			cmd.Err = nil
		}))...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(discard))
	defer cancel()
	if err := chromedp.Do(ctx); err == nil {
		t.Fatal("chromedp.Do() error = nil, want the start of the missing executable to fail")
	}
	if got.args == nil {
		t.Fatal("ModifyCmdFunc was not called")
	}
	return got
}

func TestFlags(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "browser-profile")
	headless := launchCmd(t, Options{Headless: true, ProfileDir: profileDir}).args
	headed := launchCmd(t, Options{Headless: false, ProfileDir: profileDir}).args

	t.Run("headed と headless の違いは --headless だけ", func(t *testing.T) {
		if !slices.Contains(headless, "--headless") {
			t.Errorf("headless args = %q, want --headless", headless)
		}
		if slices.Contains(headed, "--headless") {
			t.Errorf("headed args = %q, want no --headless", headed)
		}
		withoutHeadless := slices.DeleteFunc(slices.Clone(headless), func(a string) bool { return a == "--headless" })
		if !slices.Equal(withoutHeadless, headed) {
			t.Errorf("headless args without --headless = %q, want headed args %q", withoutHeadless, headed)
		}
	})

	t.Run("user-data-dir は ProfileDir", func(t *testing.T) {
		want := "--user-data-dir=" + profileDir
		for name, args := range map[string][]string{"headless": headless, "headed": headed} {
			if !slices.Contains(args, want) {
				t.Errorf("%s args = %q, want %q", name, args, want)
			}
		}
	})

	t.Run("仕様書 §38 の組 A どおり --use-mock-keychain と --password-store=basic を両方で渡す", func(t *testing.T) {
		for name, args := range map[string][]string{"headless": headless, "headed": headed} {
			for _, want := range []string{"--use-mock-keychain", "--password-store=basic"} {
				if !slices.Contains(args, want) {
					t.Errorf("%s args = %q, want %q", name, args, want)
				}
			}
		}
	})

	t.Run("ExecPath の実行ファイルで起動する", func(t *testing.T) {
		execPath := filepath.Join(t.TempDir(), "chrome")
		got := launchCmd(t, Options{Headless: true, ProfileDir: profileDir, ExecPath: execPath}).path
		if got != execPath {
			t.Errorf("cmd.Path = %q, want %q", got, execPath)
		}
	})
}

func TestOpen(t *testing.T) {
	t.Run("Chrome を起動できなければ exit 5 の種類で失敗する", func(t *testing.T) {
		dir := t.TempDir()
		b, err := Open(context.Background(), Options{
			Headless:   true,
			ProfileDir: filepath.Join(dir, "browser-profile"),
			ExecPath:   filepath.Join(dir, "no-such-chrome"),
		})
		if b != nil {
			t.Errorf("Open() browser = %v, want nil", b)
		}
		if err == nil {
			t.Fatal("Open() error = nil, want browser error")
		}
		if got := exitcode.Code(err); got != 5 {
			t.Errorf("exitcode.Code(err) = %d, want 5", got)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("errors.Is(err, os.ErrNotExist) = false, err = %v", err)
		}
	})

	t.Run("起動した Chrome がすぐ終わったら exit 5 の種類で失敗し、chromedp のログは log.Printf に出ない", func(t *testing.T) {
		logs := captureLog(t)
		b, err := Open(context.Background(), Options{
			Headless:   true,
			ProfileDir: filepath.Join(t.TempDir(), "browser-profile"),
			ExecPath:   fakeChrome(t),
		})
		if b != nil {
			t.Errorf("Open() browser = %v, want nil", b)
		}
		if got := exitcode.Code(err); got != 5 {
			t.Errorf("exitcode.Code(err) = %d, want 5 (err = %v)", got, err)
		}
		if logs.Len() != 0 {
			t.Errorf("log output = %q, want empty", logs.String())
		}
	})
}

// fakeChrome は、chromedp が --remote-debugging-pipe で渡す fd 4 に id も method も無いメッセージを 1 つ書いて終わる実行ファイルを作る。
// chromedp はこのメッセージを受けるとエラーのログを出す。
func fakeChrome(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-chrome")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '{}\\000' >&4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

func TestOpenChrome(t *testing.T) {
	execPath := chromePath(t)

	t.Run("実 Chrome でも chromedp のログを log.Printf に出さず、Close は Chrome が終わってから戻る", func(t *testing.T) {
		logs := captureLog(t)

		opts := Options{Headless: true, ProfileDir: filepath.Join(t.TempDir(), "browser-profile"), ExecPath: execPath}
		b, err := Open(context.Background(), opts)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		if err := chromedp.Do(b.ctx, chromedp.Navigate("about:blank")); err != nil {
			t.Fatalf("Navigate() error = %v", err)
		}
		proc := chromedp.FromContext(b.ctx).Browser.Process()
		if err := b.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if err := proc.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("Chrome process after Close: Signal() error = %v, want os.ErrProcessDone", err)
		}
		if _, err := os.Lstat(filepath.Join(opts.ProfileDir, "SingletonLock")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("SingletonLock after Close: Lstat() error = %v, want not exist", err)
		}

		b, err = Open(context.Background(), opts)
		if err != nil {
			t.Fatalf("Open() again with the same profile error = %v", err)
		}
		if err := b.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if logs.Len() != 0 {
			t.Errorf("log output = %q, want empty", logs.String())
		}
	})
}
