// Package secrets は Notion token を環境変数か macOS Keychain から取り出し、Keychain に保存する。
package secrets

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

// EnvNotionToken は Keychain より優先して読む Notion token の環境変数（仕様書 §41）。
const EnvNotionToken = "KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN"

// Keychain の項目。logout はこの service / account で消す。
const (
	keychainService = "kindle-highlight-sync"
	keychainAccount = "notion-token"
)

// Source は token をどこから得たか。
type Source int

const (
	SourceEnv Source = iota + 1
	SourceKeychain
)

// ErrNotFound は環境変数にも Keychain にも token が無いときの失敗。
var ErrNotFound = errors.New("notion token not found in " + EnvNotionToken + " or Keychain")

var getenv = os.Getenv

// NotionToken は環境変数、無ければ Keychain の順に Notion token を探し、得た場所も返す。
// どちらにも無ければ ErrNotFound を返す。
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

// SaveNotionToken は token を Keychain に保存する。既にあれば上書きする。
func SaveNotionToken(token string) error {
	if err := keyring.Set(keychainService, keychainAccount, token); err != nil {
		return &keychainError{op: "save", err: err}
	}
	return nil
}

// keychainError の文言は操作と service / account だけで作り、go-keyring の文言を含めない（token が混ざりうるため）。
// 元の error は Unwrap で取り出せる。
type keychainError struct {
	op  string
	err error
}

func (e *keychainError) Error() string {
	return fmt.Sprintf("cannot %s Notion token in Keychain (service %s, account %s)", e.op, keychainService, keychainAccount)
}

func (e *keychainError) Unwrap() error { return e.err }
