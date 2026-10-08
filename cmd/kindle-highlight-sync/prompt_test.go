package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type promptFixture struct {
	p      *prompter
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newPromptFixture(input io.Reader) promptFixture {
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(input)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return promptFixture{p: newPrompter(cmd), stdout: &stdout, stderr: &stderr}
}

func assertPromptIO(t *testing.T, fixture promptFixture, wantStderr string) {
	t.Helper()
	if got := fixture.stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got := fixture.stderr.String(); got != wantStderr {
		t.Errorf("stderr = %q, want %q", got, wantStderr)
	}
}

func TestPrompterLine(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       string
		wantErr    error
		wantStderr string
	}{
		{
			name:       "前後の空白を除く",
			input:      "  abc  \n",
			want:       "abc",
			wantStderr: "name: ",
		},
		{
			name:       "改行なしの入力",
			input:      "abc",
			want:       "abc",
			wantStderr: "name: ",
		},
		{
			name:       "空の入力で EOF",
			wantErr:    io.ErrUnexpectedEOF,
			wantStderr: "name: ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromptFixture(strings.NewReader(tt.input))
			got, err := fixture.p.line("name")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("line() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("line() = %q, want %q", got, tt.want)
			}
			assertPromptIO(t, fixture, tt.wantStderr)
		})
	}
}

func TestPrompterSecret(t *testing.T) {
	tests := []struct {
		name  string
		input io.Reader
		want  string
	}{
		{
			name:  "通常の入力",
			input: strings.NewReader("ntn_x\n"),
			want:  "ntn_x",
		},
		{
			name: "端末でない os.File 入力",
			input: func() io.Reader {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatalf("os.Pipe() error = %v", err)
				}
				if _, err := io.WriteString(writer, "ntn_pipe\n"); err != nil {
					t.Fatalf("WriteString() error = %v", err)
				}
				if err := writer.Close(); err != nil {
					t.Fatalf("writer.Close() error = %v", err)
				}
				t.Cleanup(func() {
					if err := reader.Close(); err != nil {
						t.Errorf("reader.Close() error = %v", err)
					}
				})
				return reader
			}(),
			want: "ntn_pipe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromptFixture(tt.input)
			if fixture.p.term != nil {
				t.Fatal("term != nil, want nil for non-terminal input")
			}
			got, err := fixture.p.secret("token")
			if err != nil {
				t.Fatalf("secret() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("secret() = %q, want %q", got, tt.want)
			}
			assertPromptIO(t, fixture, "token: ")
		})
	}
}

func TestPrompterChoose(t *testing.T) {
	if noDefault == notMapped {
		t.Fatal("noDefault and notMapped must differ")
	}

	tests := []struct {
		name       string
		options    []string
		input      string
		def        int
		optional   bool
		want       int
		wantErr    error
		wantStderr string
	}{
		{
			name:       "番号で選ぶ",
			input:      "2\n",
			def:        noDefault,
			want:       1,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: ",
		},
		{
			name:       "既定ありで空入力",
			input:      "\n",
			def:        1,
			want:       1,
			wantStderr: "choose\n  1) A\n  2) B\nnumber [2]: ",
		},
		{
			name:       "optional で対応させないを既定にする",
			input:      "\n",
			def:        notMapped,
			optional:   true,
			want:       notMapped,
			wantStderr: "choose\n  1) A\n  2) B\n  0) do not map\nnumber [0]: ",
		},
		{
			name:       "optional で対応させない既定から番号を選ぶ",
			input:      "2\n",
			def:        notMapped,
			optional:   true,
			want:       1,
			wantStderr: "choose\n  1) A\n  2) B\n  0) do not map\nnumber [0]: ",
		},
		{
			name:       "optional で既定の番号を使う",
			input:      "\n",
			def:        1,
			optional:   true,
			want:       1,
			wantStderr: "choose\n  1) A\n  2) B\n  0) do not map\nnumber [2]: ",
		},
		{
			name:       "範囲外を選び直す",
			input:      "3\n1\n",
			def:        noDefault,
			want:       0,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: enter a number from 1 to 2\nnumber: ",
		},
		{
			name:       "数字でない入力を選び直す",
			input:      "x\n2\n",
			def:        noDefault,
			want:       1,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: enter a number from 1 to 2\nnumber: ",
		},
		{
			name:       "既定なしの空入力を選び直す",
			input:      "\n1\n",
			def:        noDefault,
			want:       0,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: enter a number from 1 to 2\nnumber: ",
		},
		{
			name:       "optional で 0 を選ぶ",
			input:      "0\n",
			def:        noDefault,
			optional:   true,
			want:       notMapped,
			wantStderr: "choose\n  1) A\n  2) B\n  0) do not map\nnumber: ",
		},
		{
			name:       "optional で既定なしの空入力を選び直す",
			input:      "\n1\n",
			def:        noDefault,
			optional:   true,
			want:       0,
			wantStderr: "choose\n  1) A\n  2) B\n  0) do not map\nnumber: enter a number from 0 to 2\nnumber: ",
		},
		{
			name:       "optional でない 0 を選び直す",
			input:      "0\n2\n",
			def:        noDefault,
			want:       1,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: enter a number from 1 to 2\nnumber: ",
		},
		{
			name:       "選び直し中に EOF",
			input:      "3\n",
			def:        noDefault,
			wantErr:    io.ErrUnexpectedEOF,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: enter a number from 1 to 2\nnumber: ",
		},
		{
			name:       "最初から EOF",
			def:        noDefault,
			wantErr:    io.ErrUnexpectedEOF,
			wantStderr: "choose\n  1) A\n  2) B\nnumber: ",
		},
		{
			name:       "必須で選択肢が空",
			options:    []string{},
			input:      "1\n",
			def:        noDefault,
			want:       0,
			wantErr:    errNoOptions,
			wantStderr: "",
		},
		{
			name:       "optional で選択肢が空なら対応させないだけ選べる",
			options:    []string{},
			input:      "0\n",
			def:        noDefault,
			optional:   true,
			want:       notMapped,
			wantStderr: "choose\n  0) do not map\nnumber: ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPromptFixture(strings.NewReader(tt.input))
			options := tt.options
			if options == nil {
				options = []string{"A", "B"}
			}
			got, err := fixture.p.choose("choose", options, tt.def, tt.optional)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("choose() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("choose() = %d, want %d", got, tt.want)
			}
			assertPromptIO(t, fixture, tt.wantStderr)
		})
	}
}
