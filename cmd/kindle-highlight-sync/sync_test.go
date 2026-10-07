package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	wantSyncShort         = "Kindle の Highlight と Note を Notion に同期する"
	wantAlreadyRunningErr = "already running: another kindle-highlight-sync process is running\n"
)

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
	t.Run("ほかに sync が動いていなければ exit 0 で終わり run.lock ができる", func(t *testing.T) {
		p := setupXDG(t, "")
		code, stderr := runSync(t)
		assertSyncSucceeded(t, code, stderr, filepath.Join(p.DataDir, "run.lock"))
	})

	t.Run("先発が排他を持っている間の 2 つ目は待たずに exit 9 で終わる", func(t *testing.T) {
		p := setupXDG(t, "")
		acquired, release := make(chan struct{}), make(chan struct{})
		orig := syncBody
		syncBody = func(context.Context, *app) error {
			close(acquired)
			<-release
			return nil
		}
		t.Cleanup(func() { syncBody = orig })

		type result struct {
			code   int
			stderr string
		}
		first := make(chan result)
		go func() {
			var stdout, stderr bytes.Buffer
			code := run([]string{"sync"}, &stdout, &stderr)
			first <- result{code: code, stderr: stderr.String()}
		}()
		select {
		case <-acquired:
		case got := <-first:
			t.Fatalf("first run(sync) returned before syncBody ran: code = %d, stderr = %q", got.code, got.stderr)
		}

		code, stderr := runSync(t)
		if code != 9 {
			t.Errorf("second run(sync) code = %d, want 9", code)
		}
		if stderr != wantAlreadyRunningErr {
			t.Errorf("second run(sync) stderr = %q, want %q", stderr, wantAlreadyRunningErr)
		}

		close(release)
		got := <-first
		assertSyncSucceeded(t, got.code, got.stderr, filepath.Join(p.DataDir, "run.lock"))
	})

	t.Run("前の実行の run.lock が残っていても誰も持っていなければ exit 0 で終わる", func(t *testing.T) {
		p := setupXDG(t, "")
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
		code, stderr := runSync(t)
		if code != 3 {
			t.Errorf("run(sync) code = %d, want 3", code)
		}
		wantStderr := "configuration / confirmation required: " + p.ConfigFile + ": invalid TOML (line 1, column 8)\n"
		if stderr != wantStderr {
			t.Errorf("run(sync) stderr = %q, want %q", stderr, wantStderr)
		}
	})

	t.Run("--help の一覧に sync が出る", func(t *testing.T) {
		setupXDG(t, "")
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
			t.Errorf("run(--help) code = %d, want 0", code)
		}
		var found bool
		for line := range strings.Lines(stdout.String()) {
			if strings.HasPrefix(line, "  sync ") && strings.Contains(line, wantSyncShort) {
				found = true
			}
		}
		if !found {
			t.Errorf("--help output does not list sync: %q", stdout.String())
		}
	})
}
