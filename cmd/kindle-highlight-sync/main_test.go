package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

const (
	configFilePlaceholder = "{configFile}"
	invalidTOML           = "[amazon\nregion = \"japan\"\n"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		wantCode          int
		wantStderr        string
		wantShortOnStdout bool
	}{
		{
			name:       "存在しないサブコマンド",
			args:       []string{"no-such-command"},
			wantCode:   1,
			wantStderr: "general error: unknown command \"no-such-command\" for \"kindle-highlight-sync\"\n",
		},
		{
			name:       "不正なフラグ",
			args:       []string{"--bogus"},
			wantCode:   1,
			wantStderr: "general error: unknown flag: --bogus\n",
		},
		{
			name:              "引数なし",
			args:              []string{},
			wantShortOnStdout: true,
		},
		{
			name:              "--help",
			args:              []string{"--help"},
			wantShortOnStdout: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run() code = %d, want %d", code, tt.wantCode)
			}
			if got := stderr.String(); got != tt.wantStderr {
				t.Errorf("run() stderr = %q, want %q", got, tt.wantStderr)
			}
			if tt.wantShortOnStdout && !strings.Contains(stdout.String(), wantShort) {
				t.Errorf("stdout does not contain Short description: %q", stdout.String())
			}
		})
	}
}

func TestExecute(t *testing.T) {
	returnError := func(err error) func(*cobra.Command, []string) error {
		return func(*cobra.Command, []string) error {
			return err
		}
	}
	tests := []struct {
		name       string
		args       []string
		configTOML string
		runE       func(*cobra.Command, []string) error
		wantCode   int
		wantStderr string
	}{
		{
			name:       "Amazon authentication required",
			runE:       returnError(exitcode.New(exitcode.AmazonAuth, errors.New("boom"))),
			wantCode:   2,
			wantStderr: "Amazon authentication required: boom\n",
		},
		{
			name:       "configuration / confirmation required",
			runE:       returnError(exitcode.New(exitcode.ConfigRequired, errors.New("boom"))),
			wantCode:   3,
			wantStderr: "configuration / confirmation required: boom\n",
		},
		{
			name:       "Notion error",
			runE:       returnError(exitcode.New(exitcode.Notion, errors.New("boom"))),
			wantCode:   4,
			wantStderr: "Notion error: boom\n",
		},
		{
			name:       "browser error",
			runE:       returnError(exitcode.New(exitcode.Browser, errors.New("boom"))),
			wantCode:   5,
			wantStderr: "browser error: boom\n",
		},
		{
			name:       "local state / ID scheme error",
			runE:       returnError(exitcode.New(exitcode.LocalState, errors.New("boom"))),
			wantCode:   6,
			wantStderr: "local state / ID scheme error: boom\n",
		},
		{
			name:       "source format error",
			runE:       returnError(exitcode.New(exitcode.SourceFormat, errors.New("boom"))),
			wantCode:   7,
			wantStderr: "source format error: boom\n",
		},
		{
			name:       "partial failure",
			runE:       returnError(exitcode.New(exitcode.PartialFailure, errors.New("boom"))),
			wantCode:   8,
			wantStderr: "partial failure: boom\n",
		},
		{
			name:       "already running",
			runE:       returnError(exitcode.New(exitcode.AlreadyRunning, errors.New("boom"))),
			wantCode:   9,
			wantStderr: "already running: boom\n",
		},
		{
			name:       "general error",
			runE:       returnError(exitcode.New(exitcode.General, errors.New("boom"))),
			wantCode:   1,
			wantStderr: "general error: boom\n",
		},
		{
			name:       "plain error",
			runE:       returnError(errors.New("boom")),
			wantCode:   1,
			wantStderr: "general error: boom\n",
		},
		{
			name:       "multi-line error",
			runE:       returnError(errors.New("line1\nline2")),
			wantCode:   1,
			wantStderr: "general error: line1 line2\n",
		},
		{
			name: "panic",
			runE: func(*cobra.Command, []string) error {
				panic("boom\nntn_abc123")
			},
			wantCode:   1,
			wantStderr: "general error: panic: boom [REDACTED]\n",
		},
		{
			name:       "Notion token",
			runE:       returnError(exitcode.New(exitcode.Notion, errors.New("request failed: token=ntn_abc123"))),
			wantCode:   4,
			wantStderr: "Notion error: request failed: token=[REDACTED]\n",
		},
		{
			name:       "Authorization header",
			runE:       returnError(errors.New("request failed\nAuthorization: Bearer secret-value")),
			wantCode:   1,
			wantStderr: "general error: request failed Authorization: [REDACTED]\n",
		},
		{
			name:       "unknown command suggestion",
			args:       []string{"fial"},
			runE:       returnError(nil),
			wantCode:   1,
			wantStderr: "general error: unknown command \"fial\" for \"kindle-highlight-sync\" Did you mean this? \tfail\n",
		},
		{
			name:       "TOML として読めない config.toml",
			configTOML: invalidTOML,
			runE:       returnError(nil),
			wantCode:   3,
			wantStderr: "configuration / confirmation required: {configFile}: invalid TOML (line 1, column 8)\n",
		},
		{
			name:       "型が合わない config.toml",
			configTOML: "[amazon]\nregion = 1\n",
			runE:       returnError(nil),
			wantCode:   3,
			wantStderr: "configuration / confirmation required: {configFile}: invalid key amazon.region (line 2, column 10)\n",
		},
		{
			name:       "§58 に無い key がある config.toml",
			configTOML: "[notion]\ntoken = \"x\"\n",
			runE:       returnError(nil),
			wantCode:   3,
			wantStderr: "configuration / confirmation required: {configFile}: unknown key notion.token (line 2, column 1)\n",
		},
		{
			name:       "壊れた config.toml でも help は前処理を飛ばす",
			args:       []string{"help"},
			configTOML: invalidTOML,
			runE:       returnError(nil),
			wantCode:   0,
			wantStderr: "",
		},
		{
			name:       "壊れた config.toml でも completion bash は前処理を飛ばす",
			args:       []string{"completion", "bash"},
			configTOML: invalidTOML,
			runE:       returnError(nil),
			wantCode:   0,
			wantStderr: "",
		},
		{
			name:       "壊れた config.toml でも __complete は前処理を飛ばす",
			args:       []string{"__complete", ""},
			configTOML: invalidTOML,
			runE:       returnError(nil),
			wantCode:   0,
			wantStderr: "Completion ended with directive: ShellCompDirectiveNoFileComp\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := setupXDG(t, tt.configTOML)
			root := newRootCmd()
			root.AddCommand(&cobra.Command{Use: "fail", RunE: tt.runE})
			args := tt.args
			if args == nil {
				args = []string{"fail"}
			}
			var stdout, stderr bytes.Buffer
			code := execute(root, args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("execute() code = %d, want %d", code, tt.wantCode)
			}
			wantStderr := strings.ReplaceAll(tt.wantStderr, configFilePlaceholder, p.ConfigFile)
			if got := stderr.String(); got != wantStderr {
				t.Errorf("execute() stderr = %q, want %q", got, wantStderr)
			}
		})
	}
}
