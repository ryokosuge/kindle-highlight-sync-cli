package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shaARM64 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaAMD64 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func writeChecksums(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun(t *testing.T) {
	amd := shaAMD64 + "  kindle-highlight-sync_v1.2.3_darwin_amd64.tar.gz\n"
	arm := shaARM64 + "  kindle-highlight-sync_v1.2.3_darwin_arm64.tar.gz\n"
	tests := []struct {
		name       string
		checksums  string
		wantCode   int
		wantStdout bool
	}{
		{"正常系は本文を標準出力に出して exit 0", arm + amd, 0, true},
		{"arm64 の行が無いと標準出力が空で exit 1", amd, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeChecksums(t, tt.checksums)
			var stdout, stderr bytes.Buffer
			code := run([]string{"v1.2.3", path}, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run() = %v, want %v (stderr = %q)", code, tt.wantCode, stderr.String())
			}
			if got := strings.Contains(stdout.String(), `version "1.2.3"`); got != tt.wantStdout {
				t.Errorf("stdout に version を含む = %v, want %v (stdout = %q)", got, tt.wantStdout, stdout.String())
			}
			if !tt.wantStdout && stdout.Len() != 0 {
				t.Errorf("stdout = %q, want 空", stdout.String())
			}
			if !tt.wantStdout && !strings.Contains(stderr.String(), "darwin_arm64") {
				t.Errorf("stderr = %q, want asset 名を含む", stderr.String())
			}
		})
	}
}
