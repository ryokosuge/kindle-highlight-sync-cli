package lock

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

const helperPathEnv = "KHS_LOCK_HELPER_PATH"
const lockedLine = "locked"

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "run.lock")
}

func mustAcquire(t *testing.T, path string) *Lock {
	t.Helper()
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire(%q) error = %v", path, err)
	}
	return l
}

func assertAlreadyRunning(t *testing.T, l *Lock, err error) {
	t.Helper()
	if l != nil {
		t.Errorf("Acquire() lock = %v, want nil", l)
	}
	if err == nil {
		t.Error("Acquire() error = nil, want already-running error")
		return
	}
	if exitcode.Code(err) != 9 {
		t.Errorf("exitcode.Code(err) = %d, want 9", exitcode.Code(err))
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("errors.Is(err, ErrAlreadyRunning) = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "another kindle-highlight-sync process is running") {
		t.Errorf("err = %q, want already-running message", err)
	}
}

func TestAcquire(t *testing.T) {
	t.Run("別の取得者が持っている間は待たずに 9 で失敗する", func(t *testing.T) {
		path := lockPath(t)
		l1 := mustAcquire(t, path)
		t.Cleanup(func() {
			if err := l1.Release(); err != nil {
				t.Errorf("Release() error = %v", err)
			}
		})

		l2, err := Acquire(path)
		assertAlreadyRunning(t, l2, err)
	})

	t.Run("解放すると次の取得が成功する", func(t *testing.T) {
		path := lockPath(t)
		l1 := mustAcquire(t, path)
		if err := l1.Release(); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("os.Stat(%q) error = %v", path, err)
		}

		l2 := mustAcquire(t, path)
		if err := l2.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	})

	t.Run("前の実行の run.lock が残っていても取得が成功する", func(t *testing.T) {
		path := lockPath(t)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", path, err)
		}

		l := mustAcquire(t, path)
		if err := l.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	})

	t.Run("排他を持ったプロセスが kill -9 で終わったあと取得が成功する", func(t *testing.T) {
		path := lockPath(t)
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(), helperPathEnv+"="+path)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatalf("StdinPipe() error = %v", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("StdoutPipe() error = %v", err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		waited := false
		t.Cleanup(func() {
			if !waited {
				_ = stdin.Close()
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_ = cmd.Wait()
			}
		})

		scanner := bufio.NewScanner(stdout)
		locked := false
		for scanner.Scan() {
			if scanner.Text() == lockedLine {
				locked = true
				break
			}
		}
		if !locked {
			t.Fatalf("waiting for %q failed: %v; stderr: %s", lockedLine, scanner.Err(), stderr.String())
		}

		l, err := Acquire(path)
		assertAlreadyRunning(t, l, err)

		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("Process.Kill() error = %v", err)
		}
		_ = cmd.Wait()
		waited = true

		l = mustAcquire(t, path)
		if err := l.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	})
}

func TestHelperProcess(t *testing.T) {
	path := os.Getenv(helperPathEnv)
	if path == "" {
		return
	}
	if _, err := Acquire(path); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := fmt.Println(lockedLine); err != nil {
		os.Exit(2)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestAcquireNonContentionFailure(t *testing.T) {
	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{
			name: "ディレクトリが無い",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing", "run.lock")
			},
		},
		{
			name: "権限が無い",
			path: func(t *testing.T) string {
				dir := t.TempDir()
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatalf("os.Chmod(%q) error = %v", dir, err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(dir, 0o700); err != nil {
						t.Errorf("os.Chmod(%q) cleanup error = %v", dir, err)
					}
				})
				return filepath.Join(dir, "run.lock")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "権限が無い" && os.Geteuid() == 0 {
				t.Skip("root can write in directories without write permission")
			}
			l, err := Acquire(tt.path(t))
			if l != nil {
				t.Errorf("Acquire() lock = %v, want nil", l)
			}
			if err == nil {
				t.Fatal("Acquire() error = nil, want non-contention failure")
			}
			if exitcode.Code(err) != 1 {
				t.Errorf("exitcode.Code(err) = %d, want 1", exitcode.Code(err))
			}
			if errors.Is(err, ErrAlreadyRunning) {
				t.Errorf("errors.Is(err, ErrAlreadyRunning) = true, err = %v", err)
			}
		})
	}
}
