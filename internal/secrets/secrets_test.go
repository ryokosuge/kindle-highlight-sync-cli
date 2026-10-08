package secrets

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func setupSecrets(t *testing.T, envToken string) {
	t.Helper()
	keyring.MockInit()
	originalGetenv := getenv
	getenv = func(key string) string {
		if key == "KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN" {
			return envToken
		}
		return ""
	}
	t.Cleanup(func() {
		getenv = originalGetenv
	})
}

func TestNotionToken(t *testing.T) {
	tests := []struct {
		name         string
		envToken     string
		keychain     bool
		keychainFail error
		wantToken    string
		wantSource   Source
		wantErr      bool
	}{
		{
			name:       "環境変数が Keychain より優先される",
			envToken:   "env-token",
			keychain:   true,
			wantToken:  "env-token",
			wantSource: SourceEnv,
		},
		{
			name:       "環境変数が空なら Keychain を使う",
			keychain:   true,
			wantToken:  "keychain-token",
			wantSource: SourceKeychain,
		},
		{
			name:    "どちらにも token がない",
			wantErr: true,
		},
		{
			name:         "環境変数があれば Keychain の失敗を見ない",
			envToken:     "env-token",
			keychainFail: errors.New("keychain unavailable"),
			wantToken:    "env-token",
			wantSource:   SourceEnv,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupSecrets(t, tt.envToken)
			if tt.keychain {
				if err := keyring.Set("kindle-highlight-sync", "notion-token", "keychain-token"); err != nil {
					t.Fatalf("keyring.Set() error = %v", err)
				}
			}
			if tt.keychainFail != nil {
				keyring.MockInitWithError(tt.keychainFail)
			}

			got, source, err := NotionToken()
			if tt.wantErr {
				if !errors.Is(err, ErrNotFound) {
					t.Errorf("NotionToken() error = %v, want ErrNotFound", err)
				}
				if got != "" || source != 0 {
					t.Errorf("NotionToken() = (%q, %d), want empty token and source", got, source)
				}
				return
			}
			if err != nil {
				t.Fatalf("NotionToken() error = %v", err)
			}
			if got != tt.wantToken {
				t.Errorf("NotionToken() token = %q, want %q", got, tt.wantToken)
			}
			if source != tt.wantSource {
				t.Errorf("NotionToken() source = %d, want %d", source, tt.wantSource)
			}
		})
	}
}

func TestSaveNotionToken(t *testing.T) {
	setupSecrets(t, "")

	if err := SaveNotionToken("first-token"); err != nil {
		t.Fatalf("SaveNotionToken() error = %v", err)
	}
	got, err := keyring.Get("kindle-highlight-sync", "notion-token")
	if err != nil {
		t.Fatalf("keyring.Get() error = %v", err)
	}
	if got != "first-token" {
		t.Errorf("keyring.Get() = %q, want %q", got, "first-token")
	}
	got, source, err := NotionToken()
	if err != nil {
		t.Fatalf("NotionToken() error = %v", err)
	}
	if got != "first-token" || source != SourceKeychain {
		t.Errorf("NotionToken() = (%q, %d), want (%q, %d)", got, source, "first-token", SourceKeychain)
	}

	if err := SaveNotionToken("second-token"); err != nil {
		t.Fatalf("SaveNotionToken() error = %v", err)
	}
	got, err = keyring.Get("kindle-highlight-sync", "notion-token")
	if err != nil {
		t.Fatalf("keyring.Get() after overwrite error = %v", err)
	}
	if got != "second-token" {
		t.Errorf("keyring.Get() after overwrite = %q, want %q", got, "second-token")
	}
}

func TestKeychainError(t *testing.T) {
	token := "ntn_secret-value"
	injected := errors.New("security: " + token)
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "読み込み",
			call: func() error {
				_, _, err := NotionToken()
				return err
			},
		},
		{
			name: "保存",
			call: func() error {
				return SaveNotionToken(token)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupSecrets(t, "")
			keyring.MockInitWithError(injected)

			err := tt.call()
			if err == nil {
				t.Fatal("error = nil, want Keychain error")
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("error = %q, contains token", err)
			}
			if !errors.Is(err, injected) {
				t.Errorf("errors.Is(error, injected) = false, error = %v", err)
			}
			if errors.Is(err, ErrNotFound) {
				t.Errorf("errors.Is(error, ErrNotFound) = true, error = %v", err)
			}
		})
	}
}
