package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
)

const (
	initDatabaseRawID = "0123456789abcdef0123456789abcdef"
	initDatabaseID    = "01234567-89ab-cdef-0123-456789abcdef"
	initTokenValue    = "test-token-value"
)

type initResponse struct {
	status int
	body   string
}

type initNotionServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	paths     []string
	methods   []string
	auth      []string
	responses map[string]initResponse
	onRequest func(path string)
}

func newInitNotionServer(t *testing.T, responses map[string]initResponse) *initNotionServer {
	t.Helper()
	stub := &initNotionServer{responses: responses}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.paths = append(stub.paths, r.URL.Path)
		stub.methods = append(stub.methods, r.Method)
		stub.auth = append(stub.auth, r.Header.Get("Authorization"))
		response, ok := stub.responses[r.URL.Path]
		stub.mu.Unlock()

		if !ok {
			t.Errorf("unexpected Notion path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if stub.onRequest != nil {
			stub.onRequest(r.URL.Path)
		}
		w.WriteHeader(response.status)
		_, _ = io.WriteString(w, response.body)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func setupInit(t *testing.T, input, token, configTOML string, stub *initNotionServer) config.Paths {
	t.Helper()
	p := setupXDG(t, configTOML)
	keyring.MockInit()
	t.Setenv(secrets.EnvNotionToken, token)

	oldPrompter := newInitPrompter
	oldClient := newNotionClient
	newInitPrompter = func(cmd *cobra.Command) *prompter {
		cmd.SetIn(strings.NewReader(input))
		return newPrompter(cmd)
	}
	newNotionClient = func(token string) *notion.Client {
		return notion.New(token, notion.WithBaseURL(stub.server.URL))
	}
	t.Cleanup(func() {
		newInitPrompter = oldPrompter
		newNotionClient = oldClient
	})
	return p
}

func initDatabaseResponse(dataSources ...string) initResponse {
	var refs []string
	for i, name := range dataSources {
		refs = append(refs, fmt.Sprintf(`{"id":"ds-%d","name":%q}`, i+1, name))
	}
	return initResponse{
		status: http.StatusOK,
		body:   fmt.Sprintf(`{"object":"database","id":%q,"data_sources":[%s]}`, initDatabaseID, strings.Join(refs, ",")),
	}
}

func initDataSourceResponse(dataSourceID string, includeDate, onlyMemoKindle bool) initResponse {
	properties := []string{
		`"Name":{"id":"title","name":"Name","type":"title"}`,
	}
	if onlyMemoKindle {
		properties = append(properties, `"Memo":{"id":"memo","name":"Memo","type":"rich_text"}`)
	} else {
		properties = append(properties,
			`"Kindle ID":{"id":"kindle-id","name":"Kindle ID","type":"rich_text"}`,
			`"Author":{"id":"author","name":"Author","type":"rich_text"}`,
			`"ASIN":{"id":"asin","name":"ASIN","type":"rich_text"}`,
			`"Memo":{"id":"memo","name":"Memo","type":"rich_text"}`,
		)
	}
	if includeDate {
		properties = append(properties, `"Last Annotated At":{"id":"last-annotated-at","name":"Last Annotated At","type":"date"}`)
	}
	return initResponse{
		status: http.StatusOK,
		body:   fmt.Sprintf(`{"object":"data_source","id":%q,"properties":{%s}}`, dataSourceID, strings.Join(properties, ",")),
	}
}

type initProperty struct {
	id, name, typ string
}

func initDataSourceWith(dataSourceID string, props ...initProperty) initResponse {
	properties := make([]string, len(props))
	for i, prop := range props {
		properties[i] = fmt.Sprintf(`%q:{"id":%q,"name":%q,"type":%q}`, prop.name, prop.id, prop.name, prop.typ)
	}
	return initResponse{
		status: http.StatusOK,
		body:   fmt.Sprintf(`{"object":"data_source","id":%q,"properties":{%s}}`, dataSourceID, strings.Join(properties, ",")),
	}
}

func initRoutes(dataSource initResponse) map[string]initResponse {
	return map[string]initResponse{
		"/v1/databases/" + initDatabaseID: initDatabaseResponse("Books"),
		"/v1/data_sources/ds-1":           dataSource,
	}
}

func runInitTest(t *testing.T, args []string, input, token string, responses map[string]initResponse) (config.Paths, *initNotionServer, int, string, string) {
	t.Helper()
	stub := newInitNotionServer(t, responses)
	p := setupInit(t, input, token, "", stub)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return p, stub, code, stdout.String(), stderr.String()
}

func loadInitConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	return cfg
}

func wantInitSummary(path string) string {
	return "saved " + path + "\n" +
		"database_id: " + initDatabaseID + "\n" +
		"data source: Books\n" +
		"title: Name\nkindle_id: Kindle ID\nauthor: Author\nasin: ASIN\nlast_annotated_at: Last Annotated At\n"
}

func TestInitFieldPrompt(t *testing.T) {
	input := initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n"
	_, _, code, _, stderr := runInitTest(t, []string{"init"}, input, "", initRoutes(initDataSourceResponse("ds-1", true, false)))
	if code != 0 {
		t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
	}
	for _, field := range notion.Fields() {
		t.Run(string(field)+"の問いに用途と型が含まれる", func(t *testing.T) {
			for _, want := range []string{string(field), field.Purpose(), field.Type()} {
				if want == "" || !strings.Contains(stderr, want) {
					t.Errorf("stderr contains %q = %v, want %v", want, false, true)
				}
			}
			if prompt := fieldPrompt(field); !strings.Contains(stderr, prompt+"\n") {
				t.Errorf("stderr contains %q = %v, want %v", prompt, false, true)
			}
		})
	}
}

func TestInitInteractive(t *testing.T) {
	t.Run("token と既定の対応を保存する", func(t *testing.T) {
		input := initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n"
		p, stub, code, stdout, stderr := runInitTest(t, []string{"init"}, input, "", initRoutes(initDataSourceResponse("ds-1", true, false)))
		if code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
		}
		wantStderr := setupGuide() + "Notion token: Notion Database URL or ID: " + fieldPrompt(notion.FieldTitle) + "\n" +
			"  1) Name\nnumber [1]: " + fieldPrompt(notion.FieldKindleID) + "\n" +
			"  1) ASIN\n  2) Author\n  3) Kindle ID\n  4) Memo\nnumber [3]: " + fieldPrompt(notion.FieldAuthor) + "\n" +
			"  1) ASIN\n  2) Author\n  3) Kindle ID\n  4) Memo\n  0) do not map\nnumber [2]: " + fieldPrompt(notion.FieldASIN) + "\n" +
			"  1) ASIN\n  2) Author\n  3) Kindle ID\n  4) Memo\n  0) do not map\nnumber [1]: " + fieldPrompt(notion.FieldLastAnnotatedAt) + "\n" +
			"  1) Last Annotated At\n  0) do not map\nnumber [1]: "
		if stderr != wantStderr {
			t.Errorf("stderr = %q, want %q", stderr, wantStderr)
		}
		wantStdout := wantInitSummary(p.ConfigFile)
		if stdout != wantStdout {
			t.Errorf("stdout = %q, want %q", stdout, wantStdout)
		}
		cfg := loadInitConfig(t, p.ConfigFile)
		want := &config.Config{
			Amazon: config.Amazon{Region: initRegion},
			Notion: config.Notion{
				DatabaseID: initDatabaseID, DataSourceID: "ds-1",
				Mapping: config.Mapping{Title: "title", KindleID: "kindle-id", Author: "author", ASIN: "asin", LastAnnotatedAt: "last-annotated-at"},
			},
		}
		if *cfg != *want {
			t.Errorf("config = %+v, want %+v", *cfg, *want)
		}
		data, err := os.ReadFile(p.ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(initTokenValue)) {
			t.Error("config.toml contains Notion token")
		}
		got, err := keyring.Get("kindle-highlight-sync", "notion-token")
		if err != nil || got != initTokenValue {
			t.Errorf("keyring token = %q, %v; want %q", got, err, initTokenValue)
		}
		if len(stub.auth) != 2 || stub.auth[0] != "Bearer "+initTokenValue || stub.auth[1] != "Bearer "+initTokenValue {
			t.Errorf("Authorization headers = %v", stub.auth)
		}
		if len(stub.methods) != 2 {
			t.Fatalf("request methods = %v, want two GETs", stub.methods)
		}
		for _, method := range stub.methods {
			if method != http.MethodGet {
				t.Errorf("request method = %q, want GET", method)
			}
		}
	})

	t.Run("Keychain に値があっても入力を求めて上書きする", func(t *testing.T) {
		stub := newInitNotionServer(t, initRoutes(initDataSourceResponse("ds-1", true, false)))
		setupInit(t, initTokenValue+"\n"+initDatabaseRawID+"\n\n\n\n\n\n", "", "", stub)
		if err := keyring.Set("kindle-highlight-sync", "notion-token", "old-token"); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"init"}, &stdout, &stderr); code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr.String())
		}
		got, err := keyring.Get("kindle-highlight-sync", "notion-token")
		if err != nil || got != initTokenValue {
			t.Errorf("keyring token = %q, %v; want %q", got, err, initTokenValue)
		}
		if len(stub.auth) == 0 || stub.auth[0] != "Bearer "+initTokenValue {
			t.Errorf("Authorization headers = %v", stub.auth)
		}
	})

	t.Run("環境変数の token は保存しない", func(t *testing.T) {
		input := "https://www.notion.so/Books-" + initDatabaseRawID + "\n\n\n\n\n\n"
		_, stub, code, _, stderr := runInitTest(t, []string{"init"}, input, initTokenValue, initRoutes(initDataSourceResponse("ds-1", true, false)))
		if code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
		}
		if got, err := keyring.Get("kindle-highlight-sync", "notion-token"); !errors.Is(err, keyring.ErrNotFound) || got != "" {
			t.Errorf("keyring token = %q, %v; want not found", got, err)
		}
		if len(stub.auth) == 0 || stub.auth[0] != "Bearer "+initTokenValue {
			t.Errorf("Authorization headers = %v", stub.auth)
		}
		if strings.Contains(stderr, "Notion token: ") {
			t.Errorf("stderr unexpectedly prompts for token: %q", stderr)
		}
	})

	t.Run("案内は問いより前に出る", func(t *testing.T) {
		tests := []struct {
			name, input, token string
			args               []string
		}{
			{"token を入力する経路", initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n", "", []string{"init"}},
			{"token を環境変数から取る経路", initDatabaseRawID + "\n\n\n\n\n\n", initTokenValue, []string{"init"}},
			{"--database-id を指定する経路", initTokenValue + "\n\n\n\n\n\n", "", []string{"init", "--database-id", initDatabaseRawID}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, _, code, _, stderr := runInitTest(t, tt.args, tt.input, tt.token, initRoutes(initDataSourceResponse("ds-1", true, false)))
				if code != 0 {
					t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
				}
				if ok := strings.HasPrefix(stderr, setupGuide()); !ok {
					t.Errorf("strings.HasPrefix(stderr, setupGuide()) = %v, want %v (stderr = %q)", ok, true, stderr)
				}
				guide := strings.Index(stderr, setupGuide())
				url := strings.Index(stderr, "Notion Database URL or ID")
				if before := guide >= 0 && guide < url; url >= 0 && !before {
					t.Errorf("guide index %d < URL prompt index %d = %v, want %v", guide, url, before, true)
				}
			})
		}
	})

	t.Run("0 で既定の author を解除し、ASIN に Memo を選ぶ", func(t *testing.T) {
		input := initTokenValue + "\n" + initDatabaseRawID + "\n\n\n0\n4\n\n"
		p, _, code, stdout, stderr := runInitTest(t, []string{"init"}, input, "", initRoutes(initDataSourceResponse("ds-1", true, false)))
		if code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
		}
		want := config.Mapping{Title: "title", KindleID: "kindle-id", ASIN: "memo", LastAnnotatedAt: "last-annotated-at"}
		cfg := loadInitConfig(t, p.ConfigFile)
		if cfg.Notion.Mapping != want {
			t.Errorf("mapping = %+v, want %+v", cfg.Notion.Mapping, want)
		}
		if !strings.Contains(stdout, "author: (not mapped)\nasin: Memo\n") {
			t.Errorf("stdout = %q, want author unmapped and Memo ASIN", stdout)
		}
	})

	t.Run("型の合わない任意 Property は対応させずに知らせる", func(t *testing.T) {
		responses := initRoutes(initDataSourceWith("ds-1",
			initProperty{"title", "Name", "title"},
			initProperty{"kindle-id", "Kindle ID", "rich_text"},
			initProperty{"author", "Author", "number"},
			initProperty{"asin", "ASIN", "select"},
			initProperty{"last-annotated-at", "Last Annotated At", "rich_text"},
		))
		input := initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n"
		p, stub, code, stdout, stderr := runInitTest(t, []string{"init"}, input, "", responses)
		if code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
		}
		wantStderr := setupGuide() + "Notion token: Notion Database URL or ID: " +
			"not mapping property \"Author\" to author: type is number, want rich_text\n" +
			"not mapping property \"ASIN\" to asin: type is select, want rich_text\n" +
			"not mapping property \"Last Annotated At\" to last_annotated_at: type is rich_text, want date\n" +
			fieldPrompt(notion.FieldTitle) + "\n  1) Name\nnumber [1]: " + fieldPrompt(notion.FieldKindleID) + "\n" +
			"  1) Kindle ID\n  2) Last Annotated At\nnumber [1]: " + fieldPrompt(notion.FieldAuthor) + "\n" +
			"  1) Kindle ID\n  2) Last Annotated At\n  0) do not map\nnumber [0]: " + fieldPrompt(notion.FieldASIN) + "\n" +
			"  1) Kindle ID\n  2) Last Annotated At\n  0) do not map\nnumber [0]: " + fieldPrompt(notion.FieldLastAnnotatedAt) + "\n" +
			"  0) do not map\nnumber [0]: "
		if stderr != wantStderr {
			t.Errorf("stderr = %q, want %q", stderr, wantStderr)
		}
		wantStdout := "saved " + p.ConfigFile + "\n" +
			"database_id: " + initDatabaseID + "\n" +
			"data source: Books\n" +
			"title: Name\nkindle_id: Kindle ID\nauthor: (not mapped)\nasin: (not mapped)\nlast_annotated_at: (not mapped)\n"
		if stdout != wantStdout {
			t.Errorf("stdout = %q, want %q", stdout, wantStdout)
		}
		cfg := loadInitConfig(t, p.ConfigFile)
		if want := (config.Mapping{Title: "title", KindleID: "kindle-id"}); cfg.Notion.Mapping != want {
			t.Errorf("mapping = %+v, want %+v", cfg.Notion.Mapping, want)
		}
		data, err := os.ReadFile(p.ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("[notion.mapping]")) {
			t.Errorf("config.toml has no [notion.mapping]: %q", data)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, key := range []string{"author", "asin", "last_annotated_at"} {
				if strings.HasPrefix(trimmed, key) {
					t.Errorf("config.toml maps %q: %q", key, line)
				}
			}
		}
		if got, err := keyring.Get("kindle-highlight-sync", "notion-token"); err != nil || got != initTokenValue {
			t.Errorf("keyring token = %q, %v; want %q", got, err, initTokenValue)
		}
		for _, method := range stub.methods {
			if method != http.MethodGet {
				t.Errorf("request method = %q, want GET", method)
			}
		}
		if strings.Contains(stdout+stderr, initTokenValue) {
			t.Errorf("output contains token: stdout=%q stderr=%q", stdout, stderr)
		}
	})
}

func TestInitChoices(t *testing.T) {
	t.Run("複数 Data Source を選ぶ", func(t *testing.T) {
		responses := initRoutes(initDataSourceResponse("ds-1", true, false))
		responses["/v1/databases/"+initDatabaseID] = initDatabaseResponse("Books", "Archive")
		responses["/v1/data_sources/ds-2"] = initDataSourceResponse("ds-2", true, false)
		input := initDatabaseRawID + "\n2\n\n\n\n\n\n"
		p, stub, code, stdout, stderr := runInitTest(t, []string{"init"}, initTokenValue+"\n"+input, "", responses)
		if code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
		}
		if !strings.Contains(stdout, "data source: Archive\n") {
			t.Errorf("stdout = %q, want Archive", stdout)
		}
		if !strings.Contains(stderr, "Data Source\n  1) Books\n  2) Archive\nnumber: ") {
			t.Errorf("stderr = %q, want exact Data Source prompt", stderr)
		}
		if len(stub.paths) < 2 || stub.paths[1] != "/v1/data_sources/ds-2" {
			t.Errorf("paths = %v", stub.paths)
		}
		cfg := loadInitConfig(t, p.ConfigFile)
		if cfg.Notion.DataSourceID != "ds-2" {
			t.Errorf("DataSourceID = %q, want ds-2", cfg.Notion.DataSourceID)
		}
	})

	t.Run("既定値を変更し、候補のない date を未対応にする", func(t *testing.T) {
		responses := initRoutes(initDataSourceResponse("ds-1", false, true))
		input := initTokenValue + "\n" + initDatabaseRawID + "\n\n1\n\n1\n\n"
		p, _, code, _, stderr := runInitTest(t, []string{"init"}, input, "", responses)
		if code != 0 {
			t.Fatalf("run(init) code = %d, stderr = %q", code, stderr)
		}
		cfg := loadInitConfig(t, p.ConfigFile)
		want := config.Mapping{Title: "title", KindleID: "memo", ASIN: "memo"}
		if cfg.Notion.Mapping != want {
			t.Errorf("mapping = %+v, want %+v", cfg.Notion.Mapping, want)
		}
		if !strings.Contains(stderr, "number: ") {
			t.Errorf("stderr = %q, want a required field without default", stderr)
		}
		if !strings.Contains(stderr, "0) do not map") {
			t.Errorf("stderr = %q, want optional unmapped choice", stderr)
		}
	})
}

func TestInitConfigSafety(t *testing.T) {
	tests := []struct {
		name       string
		configTOML string
	}{
		{name: "有効な config", configTOML: validConfigTOML},
		{name: "壊れた config", configTOML: invalidTOML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newInitNotionServer(t, initRoutes(initDataSourceResponse("ds-1", true, false)))
			input := initDatabaseRawID + "\n\n\n\n\n\n"
			p := setupInit(t, input, initTokenValue, tt.configTOML, stub)
			profileFile := filepath.Join(p.DataDir, "browser-profile", "keep")
			stateFile := p.StateFile()
			if err := os.MkdirAll(filepath.Dir(profileFile), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(profileFile, []byte("profile"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stateFile, []byte("state"), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(p.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			assertDataPreserved := func() {
				t.Helper()
				if data, err := os.ReadFile(profileFile); err != nil || string(data) != "profile" {
					t.Errorf("browser-profile/keep = %q, %v", data, err)
				}
				if data, err := os.ReadFile(stateFile); err != nil || string(data) != "state" {
					t.Errorf("state.db = %q, %v", data, err)
				}
			}

			var stdout, stderr bytes.Buffer
			if code := run([]string{"init"}, &stdout, &stderr); code != 3 {
				t.Errorf("run(init) code = %d, want 3", code)
			}
			wantErr := "configuration / confirmation required: " + p.ConfigFile + " already exists: run init --force to recreate it\n"
			if stderr.String() != wantErr {
				t.Errorf("stderr = %q, want %q", stderr.String(), wantErr)
			}
			after, err := os.ReadFile(p.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Error("refused init changed config.toml")
			}
			if len(stub.paths) != 0 {
				t.Errorf("refused init made Notion requests: %v", stub.paths)
			}
			assertDataPreserved()

			stdout.Reset()
			stderr.Reset()
			if code := run([]string{"init", "--force"}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(init --force) code = %d, stderr = %q", code, stderr.String())
			}
			want := &config.Config{
				Amazon: config.Amazon{Region: initRegion},
				Notion: config.Notion{
					DatabaseID: initDatabaseID, DataSourceID: "ds-1",
					Mapping: config.Mapping{Title: "title", KindleID: "kindle-id", Author: "author", ASIN: "asin", LastAnnotatedAt: "last-annotated-at"},
				},
			}
			if got := loadInitConfig(t, p.ConfigFile); *got != *want {
				t.Errorf("config = %+v, want %+v", *got, *want)
			}
			assertDataPreserved()
		})
	}

	t.Run("確認と保存の間に config.toml が現れると保存せず exit 3", func(t *testing.T) {
		stub := newInitNotionServer(t, initRoutes(initDataSourceResponse("ds-1", true, false)))
		input := initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n"
		p := setupInit(t, input, "", "", stub)
		stub.onRequest = func(path string) {
			if path == "/v1/data_sources/ds-1" {
				if err := os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700); err != nil {
					t.Errorf("create config directory during Notion request: %v", err)
					return
				}
				if err := os.WriteFile(p.ConfigFile, []byte(validConfigTOML), 0o600); err != nil {
					t.Errorf("create config.toml during Notion request: %v", err)
				}
			}
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"init"}, &stdout, &stderr)
		if code != 3 {
			t.Errorf("run(init) code = %d, want 3", code)
		}
		wantSuffix := "configuration / confirmation required: " + p.ConfigFile + " already exists: run init --force to recreate it\n"
		if !strings.HasSuffix(stderr.String(), wantSuffix) {
			t.Errorf("stderr = %q, want suffix %q", stderr.String(), wantSuffix)
		}
		if got, err := os.ReadFile(p.ConfigFile); err != nil || !bytes.Equal(got, []byte(validConfigTOML)) {
			t.Errorf("config.toml = %q, %v; want original bytes", got, err)
		}
		if _, err := keyring.Get("kindle-highlight-sync", "notion-token"); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("keyring token error = %v, want not found", err)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want empty", stdout.String())
		}
	})
}

func TestInitYes(t *testing.T) {
	t.Run("database-id と環境 token で既定値を保存し、何も表示しない", func(t *testing.T) {
		p, _, code, stdout, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", initTokenValue, initRoutes(initDataSourceResponse("ds-1", true, false)))
		if code != 0 || stderr != "" {
			t.Fatalf("run(init --yes) code = %d, stderr = %q", code, stderr)
		}
		if want := wantInitSummary(p.ConfigFile); stdout != want {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
		cfg := loadInitConfig(t, p.ConfigFile)
		want := &config.Config{
			Amazon: config.Amazon{Region: initRegion},
			Notion: config.Notion{
				DatabaseID: initDatabaseID, DataSourceID: "ds-1",
				Mapping: config.Mapping{Title: "title", KindleID: "kindle-id", Author: "author", ASIN: "asin", LastAnnotatedAt: "last-annotated-at"},
			},
		}
		if *cfg != *want {
			t.Errorf("config = %+v, want %+v", *cfg, *want)
		}
		if got, err := keyring.Get("kindle-highlight-sync", "notion-token"); !errors.Is(err, keyring.ErrNotFound) || got != "" {
			t.Errorf("keyring token = %q, %v; want not found", got, err)
		}
	})

	t.Run("Keychain token を使う", func(t *testing.T) {
		stub := newInitNotionServer(t, initRoutes(initDataSourceResponse("ds-1", true, false)))
		setupInit(t, "", "", "", stub)
		if err := keyring.Set("kindle-highlight-sync", "notion-token", "keychain-token"); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"init", "--yes", "--database-id", initDatabaseRawID}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run(init --yes) code = %d, stderr = %q", code, stderr.String())
		}
		if len(stub.auth) == 0 || stub.auth[0] != "Bearer keychain-token" {
			t.Errorf("Authorization headers = %v", stub.auth)
		}
	})

	t.Run("型の合わない任意 Property は対応させずに知らせる", func(t *testing.T) {
		responses := initRoutes(initDataSourceWith("ds-1",
			initProperty{"title", "Name", "title"},
			initProperty{"kindle-id", "Kindle ID", "rich_text"},
			initProperty{"author", "Author", "number"},
			initProperty{"asin", "ASIN", "select"},
			initProperty{"last-annotated-at", "Last Annotated At", "rich_text"},
		))
		p, stub, code, stdout, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", initTokenValue, responses)
		if code != 0 {
			t.Fatalf("run(init --yes) code = %d, stderr = %q", code, stderr)
		}
		wantStderr := "not mapping property \"Author\" to author: type is number, want rich_text\n" +
			"not mapping property \"ASIN\" to asin: type is select, want rich_text\n" +
			"not mapping property \"Last Annotated At\" to last_annotated_at: type is rich_text, want date\n"
		if stderr != wantStderr {
			t.Errorf("stderr = %q, want %q", stderr, wantStderr)
		}
		wantStdout := "saved " + p.ConfigFile + "\n" +
			"database_id: " + initDatabaseID + "\n" +
			"data source: Books\n" +
			"title: Name\nkindle_id: Kindle ID\nauthor: (not mapped)\nasin: (not mapped)\nlast_annotated_at: (not mapped)\n"
		if stdout != wantStdout {
			t.Errorf("stdout = %q, want %q", stdout, wantStdout)
		}
		cfg := loadInitConfig(t, p.ConfigFile)
		if want := (config.Mapping{Title: "title", KindleID: "kindle-id"}); cfg.Notion.Mapping != want {
			t.Errorf("mapping = %+v, want %+v", cfg.Notion.Mapping, want)
		}
		data, err := os.ReadFile(p.ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("[notion.mapping]")) {
			t.Errorf("config.toml has no [notion.mapping]: %q", data)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, key := range []string{"author", "asin", "last_annotated_at"} {
				if strings.HasPrefix(trimmed, key) {
					t.Errorf("config.toml maps %q: %q", key, line)
				}
			}
		}
		for _, method := range stub.methods {
			if method != http.MethodGet {
				t.Errorf("request method = %q, want GET", method)
			}
		}
		if strings.Contains(stdout+stderr, initTokenValue) {
			t.Errorf("output contains token: stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("判断できない場合は token や request を残さず終了する", func(t *testing.T) {
		t.Run("database-id なし", func(t *testing.T) {
			p, stub, code, _, stderr := runInitTest(t, []string{"init", "--yes"}, "", "", map[string]initResponse{})
			if code != 3 || stderr != "configuration / confirmation required: --yes requires --database-id\n" {
				t.Errorf("code = %d, stderr = %q", code, stderr)
			}
			if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("config exists: %v", err)
			}
			if len(stub.paths) != 0 {
				t.Errorf("paths = %v, want none", stub.paths)
			}
		})
		t.Run("required default なし", func(t *testing.T) {
			p, stub, code, _, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", initTokenValue, initRoutes(initDataSourceResponse("ds-1", false, true)))
			if code != 3 || stderr != "configuration / confirmation required: no default property for kindle_id: run init without --yes to choose\n" {
				t.Errorf("code = %d, stderr = %q", code, stderr)
			}
			if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("config exists: %v", err)
			}
			if len(stub.paths) != 2 {
				t.Errorf("paths = %v, want database and data source", stub.paths)
			}
		})
		t.Run("複数 Data Source", func(t *testing.T) {
			responses := initRoutes(initDataSourceResponse("ds-1", true, false))
			responses["/v1/databases/"+initDatabaseID] = initDatabaseResponse("Books", "Archive")
			p, stub, code, _, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", initTokenValue, responses)
			want := "configuration / confirmation required: database has 2 data sources: run init without --yes to choose one\n"
			if code != 3 || stderr != want {
				t.Errorf("code = %d, stderr = %q, want %q", code, stderr, want)
			}
			if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("config exists: %v", err)
			}
			if len(stub.paths) != 1 {
				t.Errorf("paths = %v, want only database request", stub.paths)
			}
		})
		t.Run("token なし", func(t *testing.T) {
			p, stub, code, _, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", "", map[string]initResponse{})
			want := "configuration / confirmation required: notion token not found in KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN or Keychain\n"
			if code != 3 || stderr != want {
				t.Errorf("code = %d, stderr = %q, want %q", code, stderr, want)
			}
			if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("config exists: %v", err)
			}
			if len(stub.paths) != 0 {
				t.Errorf("paths = %v, want none", stub.paths)
			}
		})
	})
}

func TestInitErrors(t *testing.T) {
	t.Run("入力エラー", func(t *testing.T) {
		tests := []struct {
			name, input, want string
		}{
			{"空 token", "\n", setupGuide() + "Notion token: configuration / confirmation required: notion token must not be empty\n"},
			{"不正な database", initTokenValue + "\nnot-a-database\n", setupGuide() + "Notion token: Notion Database URL or ID: configuration / confirmation required: no Notion database ID found: enter a 32-character hex ID, a UUID, or a Notion database URL\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, stub, code, _, stderr := runInitTest(t, []string{"init"}, tt.input, "", map[string]initResponse{})
				if code != 3 || stderr != tt.want {
					t.Errorf("code = %d, stderr = %q, want %q", code, stderr, tt.want)
				}
				if len(stub.paths) != 0 {
					t.Errorf("paths = %v, want none", stub.paths)
				}
			})
		}
	})

	t.Run("必須 Property が無いか型が合わないと保存せず exit 3", func(t *testing.T) {
		missing := initDataSourceWith("ds-1",
			initProperty{"title", "Name", "title"},
			initProperty{"last-annotated-at", "Last Annotated At", "date"},
		)
		mismatched := initDataSourceWith("ds-1",
			initProperty{"title", "Name", "title"},
			initProperty{"kindle-id", "Kindle ID", "number"},
			initProperty{"memo", "Memo", "rich_text"},
			initProperty{"last-annotated-at", "Last Annotated At", "date"},
		)
		tests := []struct {
			name       string
			args       []string
			input      string
			token      string
			dataSource initResponse
			want       string
		}{
			{
				name:       "不足 interactive",
				args:       []string{"init"},
				input:      initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n",
				dataSource: missing,
				want:       setupGuide() + "Notion token: Notion Database URL or ID: configuration / confirmation required: missing property for kindle_id: need a property of type rich_text\n",
			},
			{
				name:       "不足 --yes",
				args:       []string{"init", "--yes", "--database-id", initDatabaseRawID},
				token:      initTokenValue,
				dataSource: missing,
				want:       "configuration / confirmation required: missing property for kindle_id: need a property of type rich_text\n",
			},
			{
				name:       "型違い interactive",
				args:       []string{"init"},
				input:      initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n",
				dataSource: mismatched,
				want:       setupGuide() + "Notion token: Notion Database URL or ID: configuration / confirmation required: property \"Kindle ID\" for kindle_id has type number, want rich_text\n",
			},
			{
				name:       "型違い --yes",
				args:       []string{"init", "--yes", "--database-id", initDatabaseRawID},
				token:      initTokenValue,
				dataSource: mismatched,
				want:       "configuration / confirmation required: property \"Kindle ID\" for kindle_id has type number, want rich_text\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				stub := newInitNotionServer(t, initRoutes(tt.dataSource))
				p := setupInit(t, tt.input, tt.token, "", stub)
				var stdout, stderr bytes.Buffer
				if code := run(tt.args, &stdout, &stderr); code != 3 {
					t.Errorf("run(%v) code = %d, want 3; stderr = %q", tt.args, code, stderr.String())
				}
				if stderr.String() != tt.want {
					t.Errorf("stderr = %q, want %q", stderr.String(), tt.want)
				}
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want empty", stdout.String())
				}
				if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("config exists: %v", err)
				}
				if _, err := keyring.Get("kindle-highlight-sync", "notion-token"); !errors.Is(err, keyring.ErrNotFound) {
					t.Errorf("keyring token error = %v, want not found", err)
				}
				for _, method := range stub.methods {
					if method != http.MethodGet {
						t.Errorf("request method = %q, want GET", method)
					}
				}
				if strings.Contains(stdout.String()+stderr.String(), initTokenValue) {
					t.Errorf("output contains token: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}

				stub.mu.Lock()
				stub.responses["/v1/data_sources/ds-1"] = initDataSourceResponse("ds-1", true, false)
				stub.mu.Unlock()
				stdout.Reset()
				stderr.Reset()
				if code := run(tt.args, &stdout, &stderr); code != 0 {
					t.Fatalf("run(%v) code = %d, want 0; stderr = %q", tt.args, code, stderr.String())
				}
				cfg := loadInitConfig(t, p.ConfigFile)
				want := &config.Config{
					Amazon: config.Amazon{Region: initRegion},
					Notion: config.Notion{
						DatabaseID: initDatabaseID, DataSourceID: "ds-1",
						Mapping: config.Mapping{Title: "title", KindleID: "kindle-id", Author: "author", ASIN: "asin", LastAnnotatedAt: "last-annotated-at"},
					},
				}
				if *cfg != *want {
					t.Errorf("config = %+v, want %+v", *cfg, *want)
				}
				for _, method := range stub.methods {
					if method != http.MethodGet {
						t.Errorf("request method = %q, want GET", method)
					}
				}
				if strings.Contains(stdout.String()+stderr.String(), initTokenValue) {
					t.Errorf("output contains token: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			})
		}
	})

	t.Run("database に Data Source がない", func(t *testing.T) {
		responses := map[string]initResponse{
			"/v1/databases/" + initDatabaseID: initDatabaseResponse(),
		}
		p, stub, code, _, stderr := runInitTest(t, []string{"init", "--database-id", initDatabaseRawID}, "", initTokenValue, responses)
		want := setupGuide() + "configuration / confirmation required: database has no data source\n"
		if code != 3 || stderr != want {
			t.Errorf("code = %d, stderr = %q, want %q", code, stderr, want)
		}
		if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("config exists: %v", err)
		}
		if len(stub.paths) != 1 {
			t.Errorf("paths = %v, want one database request", stub.paths)
		}
	})

	t.Run("Notion の database エラーは token を出さず exit 4", func(t *testing.T) {
		responses := map[string]initResponse{
			"/v1/databases/" + initDatabaseID: {
				status: http.StatusUnauthorized,
				body:   `{"object":"error","status":401,"code":"unauthorized","message":"API token ` + initTokenValue + ` is invalid."}`,
			},
		}
		_, _, code, stdout, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", initTokenValue, responses)
		if code != 4 {
			t.Errorf("code = %d, want 4", code)
		}
		if strings.Contains(stdout+stderr, initTokenValue) {
			t.Errorf("output contains token: stdout=%q stderr=%q", stdout, stderr)
		}
		want := "Notion error: notion: GET /v1/databases/" + initDatabaseID + ": HTTP 401 unauthorized: API token [REDACTED] is invalid.\n"
		if stderr != want {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("interactive Notion エラーでは token を保存しない", func(t *testing.T) {
		responses := map[string]initResponse{
			"/v1/databases/" + initDatabaseID: {
				status: http.StatusUnauthorized,
				body:   `{"object":"error","status":401,"code":"unauthorized","message":"API token ` + initTokenValue + ` is invalid."}`,
			},
		}
		input := initTokenValue + "\n" + initDatabaseRawID + "\n"
		p, _, code, stdout, stderr := runInitTest(t, []string{"init"}, input, "", responses)
		if code != 4 {
			t.Errorf("code = %d, want 4", code)
		}
		if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("config exists: %v", err)
		}
		if _, err := keyring.Get("kindle-highlight-sync", "notion-token"); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("keyring token error = %v, want not found", err)
		}
		if strings.Contains(stdout+stderr, initTokenValue) {
			t.Errorf("output contains token: stdout=%q stderr=%q", stdout, stderr)
		}
	})

	t.Run("Notion の Data Source エラーは config を作らない", func(t *testing.T) {
		responses := map[string]initResponse{
			"/v1/databases/" + initDatabaseID: initDatabaseResponse("Books"),
			"/v1/data_sources/ds-1":           {status: http.StatusNotFound, body: `{"object":"error","status":404,"code":"object_not_found","message":"Could not find data source."}`},
		}
		p, _, code, _, stderr := runInitTest(t, []string{"init", "--yes", "--database-id", initDatabaseRawID}, "", initTokenValue, responses)
		if code != 4 {
			t.Errorf("code = %d, want 4", code)
		}
		if _, err := os.Stat(p.ConfigFile); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("config exists: %v", err)
		}
		if strings.Contains(stderr, initTokenValue) {
			t.Errorf("stderr contains token: %q", stderr)
		}
	})

	t.Run("force と Notion エラーでは古い config を保持する", func(t *testing.T) {
		stub := newInitNotionServer(t, map[string]initResponse{
			"/v1/databases/" + initDatabaseID: {
				status: http.StatusUnauthorized,
				body:   `{"object":"error","status":401,"code":"unauthorized","message":"API token ` + initTokenValue + ` is invalid."}`,
			},
		})
		p := setupInit(t, "", initTokenValue, validConfigTOML, stub)
		before, err := os.ReadFile(p.ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"init", "--force", "--yes", "--database-id", initDatabaseRawID}, &stdout, &stderr)
		if code != 4 {
			t.Errorf("run(init --force) code = %d, want 4", code)
		}
		after, err := os.ReadFile(p.ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("failed force init changed config.toml")
		}
		if _, err := keyring.Get("kindle-highlight-sync", "notion-token"); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("keyring token error = %v, want not found", err)
		}
		if strings.Contains(stdout.String()+stderr.String(), initTokenValue) {
			t.Errorf("output contains token: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})

	t.Run("config の保存に失敗すると Keychain の token を戻す", func(t *testing.T) {
		tests := []struct {
			name, previous, wantKeychain string
		}{
			{name: "旧 token がある", previous: "old-token-value", wantKeychain: "old-token-value"},
			{name: "旧 token がない", wantKeychain: initTokenValue},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				stub := newInitNotionServer(t, initRoutes(initDataSourceResponse("ds-1", true, false)))
				input := initTokenValue + "\n" + initDatabaseRawID + "\n\n\n\n\n\n"
				p := setupInit(t, input, "", validConfigTOML, stub)
				if tt.previous != "" {
					if err := keyring.Set("kindle-highlight-sync", "notion-token", tt.previous); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(p.ConfigFile)
				if err != nil {
					t.Fatal(err)
				}

				dir := filepath.Dir(p.ConfigFile)
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(dir, 0o700); err != nil {
						t.Error(err)
					}
				})
				probe, err := os.CreateTemp(dir, "probe-*")
				if err == nil {
					probePath := probe.Name()
					if err := probe.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(probePath); err != nil {
						t.Fatal(err)
					}
					t.Skip("権限を無視して書き込めるプロセス（root など）では、書き込めないディレクトリを作れない")
				}

				var stdout, stderr bytes.Buffer
				code := run([]string{"init", "--force"}, &stdout, &stderr)
				if code != 1 {
					t.Errorf("run(init --force) code = %d, want 1; stderr = %q", code, stderr.String())
				}
				if got, err := os.ReadFile(p.ConfigFile); err != nil || !bytes.Equal(got, before) {
					t.Errorf("config.toml = %q, %v; want original bytes", got, err)
				}
				if got, err := keyring.Get("kindle-highlight-sync", "notion-token"); err != nil || got != tt.wantKeychain {
					t.Errorf("keychain token = %q, %v; want %q", got, err, tt.wantKeychain)
				}
				for _, token := range []string{initTokenValue, "old-token-value"} {
					if strings.Contains(stdout.String()+stderr.String(), token) {
						t.Errorf("output contains token %q: stdout=%q stderr=%q", token, stdout.String(), stderr.String())
					}
				}
			})
		}
	})
}
