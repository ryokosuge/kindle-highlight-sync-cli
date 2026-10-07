package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func envOf(env map[string]string) func(string) string {
	return func(k string) string {
		return env[k]
	}
}

func TestResolvePaths(t *testing.T) {
	home := t.TempDir()
	xdgConfig := t.TempDir()
	xdgData := t.TempDir()

	tests := []struct {
		name string
		env  map[string]string
		want Paths
	}{
		{
			name: "XDG の環境変数が空",
			env:  nil,
			want: Paths{
				ConfigFile: filepath.Join(home, ".config", appName, "config.toml"),
				DataDir:    filepath.Join(home, ".local", "share", appName),
			},
		},
		{
			name: "XDG の環境変数が設定されている",
			env: map[string]string{
				"XDG_CONFIG_HOME": xdgConfig,
				"XDG_DATA_HOME":   xdgData,
			},
			want: Paths{
				ConfigFile: filepath.Join(xdgConfig, appName, "config.toml"),
				DataDir:    filepath.Join(xdgData, appName),
			},
		},
		{
			name: "XDG の環境変数が相対パス",
			env: map[string]string{
				"XDG_CONFIG_HOME": "relative/config",
				"XDG_DATA_HOME":   "relative/data",
			},
			want: Paths{
				ConfigFile: filepath.Join(home, ".config", appName, "config.toml"),
				DataDir:    filepath.Join(home, ".local", "share", appName),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolvePaths(envOf(tt.env), home)
			if got != tt.want {
				t.Errorf("ResolvePaths() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDefaultPaths(t *testing.T) {
	home := t.TempDir()
	xdgConfig := t.TempDir()
	xdgData := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_DATA_HOME", xdgData)

	got, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error = %v", err)
	}
	want := Paths{
		ConfigFile: filepath.Join(xdgConfig, appName, "config.toml"),
		DataDir:    filepath.Join(xdgData, appName),
	}
	if got != want {
		t.Errorf("DefaultPaths() = %+v, want %+v", got, want)
	}
}

func TestEnsureDataDir(t *testing.T) {
	t.Run("無いときは親ディレクトリも含めて 0700 で作る", func(t *testing.T) {
		p := ResolvePaths(envOf(nil), t.TempDir())
		if err := p.EnsureDataDir(); err != nil {
			t.Fatalf("EnsureDataDir() error = %v", err)
		}

		info, err := os.Stat(p.DataDir)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", p.DataDir, err)
		}
		if !info.IsDir() {
			t.Errorf("DataDir %q is not a directory", p.DataDir)
		}
		if got := info.Mode().Perm(); got != dataDirPerm {
			t.Errorf("DataDir permission = %o, want %o", got, dataDirPerm)
		}

		parent := filepath.Dir(p.DataDir)
		parentInfo, err := os.Stat(parent)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", parent, err)
		}
		if !parentInfo.IsDir() {
			t.Errorf("parent %q is not a directory", parent)
		}

		configDir := filepath.Dir(p.ConfigFile)
		_, err = os.Stat(configDir)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) error = %v, want not exist", configDir, err)
		}
	})

	t.Run("既にあるときは中身を変えずに成功する", func(t *testing.T) {
		p := ResolvePaths(envOf(nil), t.TempDir())
		if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", p.DataDir, err)
		}
		if err := os.Chmod(p.DataDir, 0o755); err != nil {
			t.Fatalf("Chmod(%q) error = %v", p.DataDir, err)
		}
		stateFile := filepath.Join(p.DataDir, "state.db")
		if err := os.WriteFile(stateFile, []byte("existing"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", stateFile, err)
		}

		if err := p.EnsureDataDir(); err != nil {
			t.Fatalf("EnsureDataDir() error = %v", err)
		}

		content, err := os.ReadFile(stateFile)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", stateFile, err)
		}
		if got := string(content); got != "existing" {
			t.Errorf("state.db content = %q, want %q", got, "existing")
		}

		info, err := os.Stat(p.DataDir)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", p.DataDir, err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Errorf("DataDir permission = %o, want %o", got, 0o755)
		}

		entries, err := os.ReadDir(p.DataDir)
		if err != nil {
			t.Fatalf("ReadDir(%q) error = %v", p.DataDir, err)
		}
		if len(entries) != 1 {
			t.Errorf("ReadDir(%q) entries = %d, want %d", p.DataDir, len(entries), 1)
		}
	})
}
