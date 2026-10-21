package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const releaseVersion = "v0.0.0-test"

// runReleaseScript は repo root で scripts/release.sh を実行する。HOME / GOCACHE / GOMODCACHE は差し替えない。
func runReleaseScript(t *testing.T, dist string, args ...string) (stderr string, err error) {
	t.Helper()
	if testing.Short() {
		t.Skip("cross build を 2 回走らせるため -short では skip する")
	}
	for _, name := range []string{"bash", "tar", "go"} {
		if _, lookErr := exec.LookPath(name); lookErr != nil {
			t.Skipf("%s が無い環境では skip する", name)
		}
	}
	cmd := exec.Command("bash", append([]string{"scripts/release.sh"}, args...)...)
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "DIST_DIR="+dist)
	var buf bytes.Buffer
	cmd.Stderr = &buf
	err = cmd.Run()
	return buf.String(), err
}

// extractBinary は tar.gz から kindle-highlight-sync 1 つだけを取り出す。
func extractBinary(t *testing.T, archive string) []byte {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v", archive, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader(%q) error = %v", archive, err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var names []string
	var bin []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next(%q) error = %v", archive, err)
		}
		names = append(names, h.Name)
		if h.Name == "kindle-highlight-sync" {
			bin, err = io.ReadAll(tr)
			if err != nil {
				t.Fatalf("io.ReadAll(%q) error = %v", h.Name, err)
			}
		}
	}
	if len(names) != 1 || bin == nil {
		t.Fatalf("%q のエントリ = %v, want [kindle-highlight-sync] のみ", archive, names)
	}
	return bin
}

func TestReleaseScript(t *testing.T) {
	t.Run("版を渡すと darwin の 2 arch のアーカイブと checksums.txt が出る", func(t *testing.T) {
		dist := t.TempDir()
		if stderr, err := runReleaseScript(t, dist, releaseVersion); err != nil {
			t.Fatalf("release.sh error = %v, want nil (stderr: %s)", err, stderr)
		}

		sums, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
		if err != nil {
			t.Fatalf("os.ReadFile(checksums.txt) error = %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
		if len(lines) != 2 {
			t.Fatalf("checksums.txt の行数 = %v, want 2 (内容: %q)", len(lines), sums)
		}
		got := map[string]string{}
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				t.Fatalf("checksums.txt の行 %q のフィールド数 = %v, want 2", line, len(fields))
			}
			got[fields[1]] = fields[0]
		}

		cases := []struct {
			name string
			arch string
			cpu  macho.Cpu
		}{
			{"arm64 のアーカイブは arm64 の Mach-O を含み sha256 が checksums.txt と一致する", "arm64", macho.CpuArm64},
			{"amd64 のアーカイブは x86_64 の Mach-O を含み sha256 が checksums.txt と一致する", "amd64", macho.CpuAmd64},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				name := "kindle-highlight-sync_" + releaseVersion + "_darwin_" + tc.arch + ".tar.gz"
				path := filepath.Join(dist, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v", path, err)
				}
				sum := sha256.Sum256(data)
				if want := got[name]; hex.EncodeToString(sum[:]) != want {
					t.Errorf("sha256(%s) = %v, want %v", name, hex.EncodeToString(sum[:]), want)
				}

				m, err := macho.NewFile(bytes.NewReader(extractBinary(t, path)))
				if err != nil {
					t.Fatalf("macho.NewFile(%s) error = %v", name, err)
				}
				if m.Cpu != tc.cpu {
					t.Errorf("%s の Cpu = %v, want %v", name, m.Cpu, tc.cpu)
				}
			})
		}
	})

	t.Run("版を渡さないと 0 以外で止まりアーカイブを出さない", func(t *testing.T) {
		dist := t.TempDir()
		stderr, err := runReleaseScript(t, dist)
		if err == nil {
			t.Fatalf("release.sh error = nil, want 非 0 の exit code")
		}
		if !strings.Contains(stderr, "usage") {
			t.Errorf("stderr = %q, want usage を含む", stderr)
		}
		matches, _ := filepath.Glob(filepath.Join(dist, "*.tar.gz"))
		if len(matches) != 0 {
			t.Errorf("出力されたアーカイブ = %v, want なし", matches)
		}
	})
}
