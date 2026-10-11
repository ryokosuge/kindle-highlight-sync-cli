package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

func TestConfirmInitialSync(t *testing.T) {
	const (
		confirmPrompt = "2 books will be created.\n3 existing pages will be adopted.\n\nContinue? [y/N] "
		declined      = "initial sync was not confirmed"
	)
	summary := syncengine.Summary{NewBooks: 2, AdoptableMatches: 3}
	errBoom := errors.New("boom")
	tests := []struct {
		name        string
		summary     syncengine.Summary
		interactive bool
		yes         bool
		input       io.Reader
		wantCode    int
		wantErr     string
		wantStderr  string
	}{
		{
			name:        "y で進む",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("y\n"),
			wantStderr:  confirmPrompt,
		},
		{
			name:        "yes で進む",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("yes\n"),
			wantStderr:  confirmPrompt,
		},
		{
			name:        "大文字の Y で進む",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("Y\n"),
			wantStderr:  confirmPrompt,
		},
		{
			name:        "大文字小文字の混ざった YeS で進む",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("YeS\n"),
			wantStderr:  confirmPrompt,
		},
		{
			name:        "改行なしの y で進む",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("y"),
			wantStderr:  confirmPrompt,
		},
		{
			name:        "空行は既定の No",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("\n"),
			wantCode:    3,
			wantErr:     declined,
			wantStderr:  confirmPrompt,
		},
		{
			name:        "n で止まる",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("n\n"),
			wantCode:    3,
			wantErr:     declined,
			wantStderr:  confirmPrompt,
		},
		{
			name:        "y 以外の文字で止まる",
			summary:     summary,
			interactive: true,
			input:       strings.NewReader("yy\n"),
			wantCode:    3,
			wantErr:     declined,
			wantStderr:  confirmPrompt,
		},
		{
			name:        "EOF で止まる",
			summary:     summary,
			interactive: true,
			wantCode:    3,
			wantErr:     declined,
			wantStderr:  confirmPrompt,
		},
		{
			name:        "読み込みエラーはそのまま返す",
			summary:     summary,
			interactive: true,
			input:       iotest.ErrReader(errBoom),
			wantCode:    1,
			wantErr:     "boom",
			wantStderr:  confirmPrompt,
		},
		{
			name:        "Create だけでも問う",
			summary:     syncengine.Summary{NewBooks: 1},
			interactive: true,
			input:       strings.NewReader("y\n"),
			wantStderr:  "1 books will be created.\n0 existing pages will be adopted.\n\nContinue? [y/N] ",
		},
		{
			name:        "Adopt だけでも問う",
			summary:     syncengine.Summary{AdoptableMatches: 1},
			interactive: true,
			input:       strings.NewReader("y\n"),
			wantStderr:  "0 books will be created.\n1 existing pages will be adopted.\n\nContinue? [y/N] ",
		},
		{
			name:        "Ambiguous は数えない",
			summary:     syncengine.Summary{NewBooks: 1, ExactMatches: 4, Ambiguous: 5, HighlightsToAppend: 9},
			interactive: true,
			input:       strings.NewReader("y\n"),
			wantStderr:  "1 books will be created.\n0 existing pages will be adopted.\n\nContinue? [y/N] ",
		},
		{
			name:        "Ambiguous だけなら問わない",
			summary:     syncengine.Summary{Ambiguous: 5, ExactMatches: 2},
			interactive: true,
			input:       strings.NewReader("n\n"),
			wantStderr:  "",
		},
		{
			name:        "0 件なら問わない",
			interactive: true,
			input:       strings.NewReader("n\n"),
			wantStderr:  "",
		},
		{
			name:        "--yes なら問わない",
			summary:     summary,
			interactive: true,
			yes:         true,
			input:       strings.NewReader("n\n"),
			wantStderr:  "",
		},
		{
			name:        "--yes なら端末でなくても問わない",
			summary:     summary,
			yes:         true,
			interactive: false,
			wantStderr:  "",
		},
		{
			name:       "端末でなければ問わずに exit 3",
			summary:    summary,
			wantCode:   3,
			wantErr:    "initial sync will create 2 books and adopt 3 existing pages: run sync with --yes to confirm without a terminal",
			wantStderr: "",
		},
		{
			name:       "端末でなく 0 件なら進む",
			wantStderr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input
			if input == nil {
				input = strings.NewReader("")
			}
			fixture := newPromptFixture(input)
			err := confirmInitialSync(fixture.p, tt.summary, tt.interactive, tt.yes)
			if got := exitcode.Code(err); got != tt.wantCode {
				t.Errorf("exitcode.Code(confirmInitialSync()) = %d, want %d", got, tt.wantCode)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("confirmInitialSync() error = %v, want nil", err)
				}
			} else if err == nil {
				t.Errorf("confirmInitialSync() error = nil, want %q", tt.wantErr)
			} else if got := err.Error(); got != tt.wantErr {
				t.Errorf("confirmInitialSync() error = %v, want %q", got, tt.wantErr)
			}
			assertPromptIO(t, fixture, tt.wantStderr)
		})
	}
}
