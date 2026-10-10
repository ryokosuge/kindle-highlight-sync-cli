package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
)

var testNotionConfig = config.Notion{
	DataSourceID: "ds-79",
	Mapping: config.Mapping{
		Title:    "title",
		KindleID: "kid%3A",
		Author:   "aut%3B",
		ASIN:     "asn%3C",
	},
}

type recordedRequest struct {
	method        string
	path          string
	authorization string
}

// newNotionServer は受けた要求を記録し、body をそのまま返す Notion の代役と、記録を返す関数を作る。
func newNotionServer(t *testing.T, body string) (*httptest.Server, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, recordedRequest{method: r.Method, path: r.URL.EscapedPath(), authorization: r.Header.Get("Authorization")})
		mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), requests...)
	}
}

// stubNewNotionClient は newNotionClient を srv に向かう Client を返す関数に差し替え、呼び出し回数を返す関数を返す。
func stubNewNotionClient(t *testing.T, srv *httptest.Server) func() int {
	t.Helper()
	calls := 0
	original := newNotionClient
	newNotionClient = func(token string) *notion.Client {
		calls++
		return notion.New(token, notion.WithBaseURL(srv.URL))
	}
	t.Cleanup(func() { newNotionClient = original })
	return func() int { return calls }
}

// pagePropertyJSON は page の Property の JSON。typ は "title" か "rich_text"。
func pagePropertyJSON(name, id, typ, text string) string {
	return fmt.Sprintf(`%q:{"id":%q,"type":%q,%q:[{"plain_text":%q}]}`, name, id, typ, typ, text)
}

// bookPageJSON は Kindle ID Property を持つ page の JSON。
func bookPageJSON(id, kindleID string) string {
	return fmt.Sprintf(`{"object":"page","id":%q,"properties":{%s}}`, id,
		pagePropertyJSON("Kindle ID", "kid%3A", "rich_text", kindleID))
}

// queryBody は Query a data source の応答の JSON。
func queryBody(pages ...string) string {
	return fmt.Sprintf(`{"object":"list","results":[%s],"has_more":false,"next_cursor":null}`, strings.Join(pages, ","))
}

func TestNewDestination_Errors(t *testing.T) {
	tests := []struct {
		name         string
		envToken     string
		saveToken    *string
		config       *config.Config
		wantMessage  func(configFile string) string
		wantNotFound bool
	}{
		{
			name:     "config.toml が無い",
			envToken: "test-token-value",
			config:   nil,
			wantMessage: func(configFile string) string {
				return configFile + " not found: run `kindle-highlight-sync init` to create it"
			},
		},
		{
			name:     "token が環境変数にも Keychain にも無い",
			envToken: "",
			config:   &config.Config{Notion: testNotionConfig},
			wantMessage: func(string) string {
				return "notion token not found in KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN or Keychain: run `kindle-highlight-sync init` to save it"
			},
			wantNotFound: true,
		},
		{
			name:      "Keychain の値が空",
			envToken:  "",
			saveToken: new(string),
			config:    &config.Config{Notion: testNotionConfig},
			wantMessage: func(string) string {
				return "notion token not found in KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN or Keychain: run `kindle-highlight-sync init` to save it"
			},
			wantNotFound: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			t.Setenv(secrets.EnvNotionToken, tt.envToken)
			if tt.saveToken != nil {
				if err := secrets.SaveNotionToken(*tt.saveToken); err != nil {
					t.Fatalf("SaveNotionToken() error = %v", err)
				}
			}
			srv, requests := newNotionServer(t, queryBody())
			calls := stubNewNotionClient(t, srv)

			configFile := filepath.Join(t.TempDir(), "config.toml")
			a := &app{paths: config.Paths{ConfigFile: configFile}, config: tt.config}

			dest, err := newDestination(a)
			if got := exitcode.Code(err); got != 3 {
				t.Errorf("exitcode.Code(err) = %d, want 3 (err = %v)", got, err)
			}
			if err == nil {
				t.Fatalf("err = nil, want %q", tt.wantMessage(configFile))
			}
			if got := err.Error(); got != tt.wantMessage(configFile) {
				t.Errorf("err.Error() = %q, want %q", got, tt.wantMessage(configFile))
			}
			if dest != nil {
				t.Errorf("dest = %v, want nil", dest)
			}
			if strings.Contains(err.Error(), "test-token-value") {
				t.Errorf("err.Error() = %q, want not to contain the token", err.Error())
			}
			if tt.wantNotFound && !errors.Is(err, secrets.ErrNotFound) {
				t.Errorf("errors.Is(err, secrets.ErrNotFound) = false, want true")
			}
			if got := calls(); got != 0 {
				t.Errorf("newNotionClient calls = %d, want 0", got)
			}
			if got := requests(); len(got) != 0 {
				t.Errorf("requests = %v, want 0 requests", got)
			}
		})
	}
}

func TestNewDestination(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		envToken bool
	}{
		{name: "環境変数の token", token: "test-token-env-79", envToken: true},
		{name: "Keychain の token", token: "test-token-keychain-79", envToken: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			if tt.envToken {
				t.Setenv(secrets.EnvNotionToken, tt.token)
			} else {
				t.Setenv(secrets.EnvNotionToken, "")
				if err := secrets.SaveNotionToken(tt.token); err != nil {
					t.Fatalf("SaveNotionToken() error = %v", err)
				}
			}
			srv, requests := newNotionServer(t, queryBody(bookPageJSON("page-79", "book-79-id")))
			stubNewNotionClient(t, srv)

			configFile := filepath.Join(t.TempDir(), "config.toml")
			a := &app{paths: config.Paths{ConfigFile: configFile}, config: &config.Config{Notion: testNotionConfig}}

			dest, err := newDestination(a)
			if err != nil {
				t.Fatalf("newDestination() error = %v", err)
			}
			if dest == nil {
				t.Fatal("dest = nil, want a Destination")
			}
			if got := requests(); len(got) != 0 {
				t.Fatalf("requests = %v, want 0 requests", got)
			}

			match, err := dest.FindBook(context.Background(), domain.Book{ID: "book-79-id", Title: "Go", Author: "Bob"})
			if err != nil {
				t.Fatalf("FindBook() error = %v", err)
			}
			if got := match.Status; got != destination.BookMatchExact {
				t.Errorf("match.Status = %q, want %q", got, destination.BookMatchExact)
			}
			if match.Book == nil {
				t.Fatal("match.Book = nil, want the page")
			}
			if got := match.Book.ID; got != "page-79" {
				t.Errorf("match.Book.ID = %q, want %q", got, "page-79")
			}
			got := requests()
			if len(got) != 1 {
				t.Fatalf("requests = %v, want 1 request", got)
			}
			if want := "POST"; got[0].method != want {
				t.Errorf("requests[0].method = %q, want %q", got[0].method, want)
			}
			if want := "/v1/data_sources/ds-79/query"; got[0].path != want {
				t.Errorf("requests[0].path = %q, want %q", got[0].path, want)
			}
			if want := "Bearer " + tt.token; got[0].authorization != want {
				t.Errorf("requests[0].authorization = %q, want %q", got[0].authorization, want)
			}
		})
	}
}
