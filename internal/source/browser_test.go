package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

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

func TestClose(t *testing.T) {
	t.Run("Browser.close の応答を待つ間に Open の ctx が取り消されても、Chrome が終わってから nil で戻る", func(t *testing.T) {
		execPath, closing := fakeCDPChrome(t, "hang")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		b, err := Open(ctx, Options{Headless: true, ProfileDir: filepath.Join(t.TempDir(), "browser-profile"), ExecPath: execPath})
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		proc := chromedp.FromContext(b.ctx).Browser.Process()

		go func() {
			waitFile(closing)
			cancel()
		}()
		if err := b.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
		if err := proc.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("Chrome process after Close: Signal() error = %v, want os.ErrProcessDone", err)
		}
	})

	t.Run("Chrome が Browser.close をエラーで返したら、Chrome が終わってから exit 5 の種類で失敗する", func(t *testing.T) {
		execPath, _ := fakeCDPChrome(t, "error")
		b, err := Open(context.Background(), Options{Headless: true, ProfileDir: filepath.Join(t.TempDir(), "browser-profile"), ExecPath: execPath})
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		proc := chromedp.FromContext(b.ctx).Browser.Process()

		err = b.Close()
		if got := exitcode.Code(err); got != 5 {
			t.Errorf("exitcode.Code(err) = %d, want 5 (err = %v)", got, err)
		}
		if err := proc.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("Chrome process after Close: Signal() error = %v, want os.ErrProcessDone", err)
		}
	})
}

// fakeCDPEnv が付いて起動されたテストバイナリは、TestFakeCDPChrome で偽の Chrome として振る舞う。
const fakeCDPEnv = "KHS_FAKE_CDP_CLOSE"

// fakeCDPChrome は、chromedp が Open で送る CDP に応じる偽 Chrome を作る。
// Browser.close を受けると closing のファイルを作り、closeMode が "hang" なら応じず、"error" ならエラーで応じる。
func fakeCDPChrome(t *testing.T, closeMode string) (execPath, closing string) {
	t.Helper()
	dir := t.TempDir()
	execPath = filepath.Join(dir, "fake-chrome")
	closing = filepath.Join(dir, "closing")
	script := fmt.Sprintf("#!/bin/sh\n%s=%s KHS_FAKE_CDP_CLOSING=%s exec %s -test.run='^TestFakeCDPChrome$'\n", fakeCDPEnv, closeMode, closing, os.Args[0])
	if err := os.WriteFile(execPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return execPath, closing
}

func waitFile(path string) {
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestFakeCDPChrome は fakeCDPChrome から起動されたときだけ、fd 3 で CDP を受けて fd 4 に返す。
func TestFakeCDPChrome(t *testing.T) {
	closeMode := os.Getenv(fakeCDPEnv)
	if closeMode == "" {
		return
	}
	in := bufio.NewReader(os.NewFile(3, "cdp-in"))
	out := os.NewFile(4, "cdp-out")
	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		if _, err := out.Write(append(b, 0)); err != nil {
			os.Exit(0)
		}
	}
	for {
		raw, err := in.ReadBytes(0)
		if err != nil {
			os.Exit(0)
		}
		var msg struct {
			ID        int64  `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId,omitempty"`
		}
		if err := json.Unmarshal(raw[:len(raw)-1], &msg); err != nil {
			panic(err)
		}
		reply := map[string]any{"id": msg.ID, "result": map[string]any{}}
		if msg.SessionID != "" {
			reply["sessionId"] = msg.SessionID
		}
		switch msg.Method {
		case "Target.attachToTarget":
			reply["result"] = map[string]any{"sessionId": "fake-session"}
		case "Runtime.evaluate":
			reply["result"] = map[string]any{"result": map[string]any{"type": "object", "className": "Window"}}
		case "Browser.close":
			if err := os.WriteFile(os.Getenv("KHS_FAKE_CDP_CLOSING"), nil, 0o600); err != nil {
				panic(err)
			}
			if closeMode == "hang" {
				continue
			}
			delete(reply, "result")
			reply["error"] = map[string]any{"code": -32000, "message": "fake close failure"}
		}
		send(reply)
		if msg.Method == "Target.setDiscoverTargets" && msg.SessionID == "" {
			send(map[string]any{
				"method": "Target.targetCreated",
				"params": map[string]any{"targetInfo": map[string]any{
					"targetId": "fake-target", "type": "page", "title": "", "url": "about:blank",
					"attached": false, "canAccessOpener": false,
				}},
			})
		}
	}
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
