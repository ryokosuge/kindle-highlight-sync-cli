package config

import (
	"os"
	"path/filepath"
)

const appName = "kindle-highlight-sync"

const dataDirPerm os.FileMode = 0o700

type Paths struct {
	ConfigFile string // <config-dir>/kindle-highlight-sync/config.toml
	DataDir    string // <data-dir>/kindle-highlight-sync
}

// ResolvePaths は §57 の場所を返す。getenv と home はテストから差し替えられるように引数で受ける。
func ResolvePaths(getenv func(string) string, home string) Paths {
	configBase := xdgBase(getenv, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataBase := xdgBase(getenv, "XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	return Paths{
		ConfigFile: filepath.Join(configBase, appName, "config.toml"),
		DataDir:    filepath.Join(dataBase, appName),
	}
}

func xdgBase(getenv func(string) string, key, fallback string) string {
	value := getenv(key)
	if filepath.IsAbs(value) {
		return value
	}
	return fallback
}

// DefaultPaths は os.Getenv と os.UserHomeDir で ResolvePaths を呼ぶ。
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return ResolvePaths(os.Getenv, home), nil
}

// EnsureDataDir は p.DataDir が無ければ 0700 で作る（os.MkdirAll）。
func (p Paths) EnsureDataDir() error {
	return os.MkdirAll(p.DataDir, dataDirPerm)
}
