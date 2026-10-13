package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

func TestFailureKind_String(t *testing.T) {
	tests := []struct {
		name string
		kind FailureKind
		want string
	}{
		{"ASIN が無い", FailureNoASIN, "book has no ASIN"},
		{"Notebook の構造の異常", FailureNotebookStructure, "unexpected notebook structure"},
		{"Highlight ID が作れない", FailureHighlightID, "highlight ID cannot be made"},
		{"ambiguous", FailureAmbiguous, "ambiguous"},
		{"Notion への書き込みの失敗", FailureNotionWrite, "notion write failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyFetchError(t *testing.T) {
	format := exitcode.New(exitcode.SourceFormat, fmt.Errorf("row: %w", source.ErrSourceFormat))
	idErr := fmt.Errorf("source: row 1: %w: %w", domain.ErrMissingAnnotationID, format)
	frame := fmt.Errorf("source: highlights page: %w: %w", source.ErrHighlightsFrame, format)
	tests := []struct {
		name     string
		err      error
		wantKind FailureKind
		wantOK   bool
	}{
		{"構造の異常は Notebook の構造の異常", format, FailureNotebookStructure, true},
		{"行 id が無い error は ErrSourceFormat も持つが Highlight ID", idErr, FailureHighlightID, true},
		{"枠の異常は run を止める", frame, 0, false},
		{"ctx の取消は run を止める", fmt.Errorf("fetch: %w", context.Canceled), 0, false},
		{"種類の無い error は run を止める", errors.New("browser closed"), 0, false},
		{"ブラウザ由来の exit 5 は run を止める", exitcode.New(exitcode.Browser, errors.New("chrome")), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := ClassifyFetchError(tt.err)
			if kind != tt.wantKind || ok != tt.wantOK {
				t.Errorf("ClassifyFetchError() = (%v, %v), want (%v, %v)", kind, ok, tt.wantKind, tt.wantOK)
			}
		})
	}
}

func TestNewFailedBooksError(t *testing.T) {
	err := NewFailedBooksError([]FailedBook{
		{BookID: "B001", Kind: FailureNotebookStructure},
		{BookID: "b1-abc", Kind: FailureNoASIN},
		{BookID: "B002", Kind: FailureNotionWrite},
	})
	if got := exitcode.Code(err); got != int(exitcode.PartialFailure) {
		t.Errorf("exitcode.Code() = %d, want %d", got, exitcode.PartialFailure)
	}
	want := "partial failure: sync: 3 books were not synced: B001 (unexpected notebook structure); b1-abc (book has no ASIN); B002 (notion write failed)"
	if got := exitcode.Describe(err); got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
	if strings.ContainsAny(exitcode.Describe(err), "\r\n") {
		t.Errorf("Describe() = %q, want a single line", exitcode.Describe(err))
	}
}
