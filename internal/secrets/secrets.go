// Package secrets は Notion token を環境変数か macOS Keychain から取り出し、Keychain に保存する。
package secrets

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

const EnvNotionToken = "KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN"

const (
	keychainService = "kindle-highlight-sync"
	keychainAccount = "notion-token"
)

type Source int

const (
	SourceEnv Source = iota + 1
	SourceKeychain
)

var ErrNotFound = errors.New("notion token not found in " + EnvNotionToken + " or Keychain")

var getenv = os.Getenv

func NotionToken() (string, Source, error) {
	if token := getenv(EnvNotionToken); token != "" {
		return token, SourceEnv, nil
	}
	token, err := keyring.Get(keychainService, keychainAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, &keychainError{op: "read", err: err}
	}
	return token, SourceKeychain, nil
}

func SaveNotionToken(token string) error {
	if err := keyring.Set(keychainService, keychainAccount, token); err != nil {
		return &keychainError{op: "save", err: err}
	}
	return nil
}

type keychainError struct {
	op  string
	err error
}

func (e *keychainError) Error() string {
	return fmt.Sprintf("cannot %s Notion token in Keychain (service %s, account %s)", e.op, keychainService, keychainAccount)
}

func (e *keychainError) Unwrap() error { return e.err }
