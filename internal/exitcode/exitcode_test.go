package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "一般", err: New(General, errors.New("boom")), want: 1},
		{name: "Amazon認証", err: New(AmazonAuth, errors.New("boom")), want: 2},
		{name: "設定", err: New(ConfigRequired, errors.New("boom")), want: 3},
		{name: "Notion", err: New(Notion, errors.New("boom")), want: 4},
		{name: "ブラウザー", err: New(Browser, errors.New("boom")), want: 5},
		{name: "ローカル状態", err: New(LocalState, errors.New("boom")), want: 6},
		{name: "ソース形式", err: New(SourceFormat, errors.New("boom")), want: 7},
		{name: "一部失敗", err: New(PartialFailure, errors.New("boom")), want: 8},
		{name: "実行中", err: New(AlreadyRunning, errors.New("boom")), want: 9},
		{
			name: "fmt.Errorf で多重に包む",
			err:  fmt.Errorf("sync: %w", fmt.Errorf("query: %w", New(Notion, errors.New("boom")))),
			want: 4,
		},
		{name: "種類なし", err: errors.New("boom"), want: 1},
		{name: "nil", err: nil, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("Code() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{name: "一般", kind: General, want: "general error"},
		{name: "Amazon認証", kind: AmazonAuth, want: "Amazon authentication required"},
		{name: "設定", kind: ConfigRequired, want: "configuration / confirmation required"},
		{name: "Notion", kind: Notion, want: "Notion error"},
		{name: "ブラウザー", kind: Browser, want: "browser error"},
		{name: "ローカル状態", kind: LocalState, want: "local state / ID scheme error"},
		{name: "ソース形式", kind: SourceFormat, want: "source format error"},
		{name: "一部失敗", kind: PartialFailure, want: "partial failure"},
		{name: "実行中", kind: AlreadyRunning, want: "already running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "Notion", err: New(Notion, errors.New("query failed")), want: "Notion error: query failed"},
		{
			name: "多重に包む",
			err:  fmt.Errorf("sync: %w", New(Notion, errors.New("query failed"))),
			want: "Notion error: sync: query failed",
		},
		{
			name: "改行を畳む",
			err:  New(Browser, errors.New("line1\nline2\r\nline3\n")),
			want: "browser error: line1 line2 line3",
		},
		{name: "種類なし", err: errors.New("oops"), want: "general error: oops"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Describe(tt.err); got != tt.want {
				t.Errorf("Describe() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	base := errors.New("boom")
	err := fmt.Errorf("x: %w", New(Notion, base))
	if got := errors.Is(err, base); !got {
		t.Errorf("errors.Is() = %v, want %v", got, true)
	}
}
