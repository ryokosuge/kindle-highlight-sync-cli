package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
)

const wantShort = "Kindle の Highlight と Note を Notion Books Database に同期する"

const validConfigTOML = `[amazon]
region = "japan"

[notion]
database_id = "db"
data_source_id = "ds"

[notion.mapping]
title = "title"
author = "%3Axs"
asin = "Ab12"
kindle_id = "Cd34"
last_annotated_at = "Ef56"
`

// setupXDG は XDG_CONFIG_HOME / XDG_DATA_HOME を一時ディレクトリに向け、configTOML が空でなければ config.toml に書く。返す Paths は §57 の場所をテスト側で組み立てたもの。
func setupXDG(t *testing.T, configTOML string) config.Paths {
	t.Helper()
	configHome, dataHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	p := config.Paths{
		ConfigFile: filepath.Join(configHome, "kindle-highlight-sync", "config.toml"),
		DataDir:    filepath.Join(dataHome, "kindle-highlight-sync"),
	}
	if configTOML != "" {
		if err := os.MkdirAll(filepath.Dir(p.ConfigFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.ConfigFile, []byte(configTOML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestRootCmd(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "引数なし", args: []string{}},
		{name: "--help", args: []string{"--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupXDG(t, "")
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(out.String(), wantShort) {
				t.Errorf("output does not contain Short description: %q", out.String())
			}
			if _, err := os.Stat(p.DataDir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Stat(%q) error = %v, want not exist (--help and no arguments must not create the data directory)", p.DataDir, err)
			}
		})
	}
}

func TestRootCmdVersion(t *testing.T) {
	tests := []struct {
		name    string
		newRoot func() *cobra.Command
		want    string
	}{
		{
			name:    "版 v1.2.3 を与えると v1.2.3 が出る",
			newRoot: func() *cobra.Command { return newRootCmdWith(&app{}, "v1.2.3") },
			want:    "kindle-highlight-sync version v1.2.3\n",
		},
		{
			name:    "版を与えない root command では dev が出る",
			newRoot: newRootCmd,
			want:    "kindle-highlight-sync version dev\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupXDG(t, "")
			var out, errOut bytes.Buffer
			if code := execute(tt.newRoot(), []string{"--version"}, &out, &errOut); code != 0 {
				t.Fatalf("exit code = %v, want 0 (stderr: %q)", code, errOut.String())
			}
			if out.String() != tt.want {
				t.Errorf("stdout = %q, want %q", out.String(), tt.want)
			}
			for _, path := range []string{p.DataDir, p.ConfigFile, p.StateFile()} {
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("Stat(%q) error = %v, want not exist (--version must not create files)", path, err)
				}
			}
		})
	}
}

func TestRootCmdPreflight(t *testing.T) {
	tests := []struct {
		name       string
		configTOML string
		want       *config.Config
	}{
		{
			name:       "config.toml あり",
			configTOML: validConfigTOML,
			want: &config.Config{
				Amazon: config.Amazon{Region: "japan"},
				Notion: config.Notion{
					DatabaseID:   "db",
					DataSourceID: "ds",
					Mapping: config.Mapping{
						Title:           "title",
						Author:          "%3Axs",
						ASIN:            "Ab12",
						KindleID:        "Cd34",
						LastAnnotatedAt: "Ef56",
					},
				},
			},
		},
		{name: "config.toml なし"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupXDG(t, tt.configTOML)
			a := &app{}
			root := newRootCmdWith(a, "dev")
			var got app
			root.AddCommand(&cobra.Command{
				Use: "probe",
				RunE: func(*cobra.Command, []string) error {
					got = *a
					return nil
				},
			})
			root.SetArgs([]string{"probe"})
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got.paths != p {
				t.Errorf("paths = %+v, want %+v", got.paths, p)
			}
			if tt.want == nil {
				if got.config != nil {
					t.Errorf("config = %+v, want nil", got.config)
				}
			} else if got.config == nil {
				t.Error("config = nil, want non-nil")
			} else if *got.config != *tt.want {
				t.Errorf("config = %+v, want %+v", *got.config, *tt.want)
			}
			info, err := os.Stat(p.DataDir)
			if err != nil {
				t.Fatalf("Stat(%q) error = %v", p.DataDir, err)
			}
			if !info.IsDir() {
				t.Errorf("DataDir %q is not a directory", p.DataDir)
			}
		})
	}
}
