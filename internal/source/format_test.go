package source

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

func TestNewFormatError(t *testing.T) {
	const what = "notebook root container is missing"
	tests := []struct {
		name string
		err  error
	}{
		{"そのまま", newFormatError(what)},
		{"呼び出し側が文脈を足す", fmt.Errorf("source: list books: page 1: %w", fmt.Errorf("source: library page: %w", newFormatError(what)))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitcode.Code(tt.err); got != 7 {
				t.Errorf("exitcode.Code(err) = %d, want 7", got)
			}
			if !errors.Is(tt.err, ErrSourceFormat) {
				t.Errorf("errors.Is(err, ErrSourceFormat) = false, err = %v", tt.err)
			}
			if msg := tt.err.Error(); !strings.HasSuffix(msg, what+": unexpected notebook structure") {
				t.Errorf("err = %q, want it to end with %q", msg, what+": unexpected notebook structure")
			}
		})
	}
}

func TestNewFormatErrorMessage(t *testing.T) {
	if got, want := newFormatError("next page token input is missing").Error(), "next page token input is missing: unexpected notebook structure"; got != want {
		t.Errorf("newFormatError().Error() = %q, want %q", got, want)
	}
}
