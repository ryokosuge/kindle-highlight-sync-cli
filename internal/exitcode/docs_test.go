package exitcode

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const docsGlob = "../../docs/*.md"

// unknownExitHeadings は doc の「### exit N:」見出しのうち、Kind に定義の無い値を返す。
// 地の文の `exit 0` など見出しでない記述は拾わない。
func unknownExitHeadings(doc string) []int {
	var unknown []int
	for code := range exitHeadings(doc) {
		if _, ok := kindNames[Kind(code)]; !ok {
			unknown = append(unknown, code)
		}
	}
	return unknown
}

// exitRowMismatch は表の「| 値 | 名前 |」の行で、値と名前の組が Kind.String() と食い違うもの。
type exitRowMismatch struct {
	code int
	name string
}

// mismatchedExitRows は doc の表の行のうち、値と名前の組が Kind に定義した組と食い違うものを返す。
// 値が Kind に無い行も食い違いとして返す。
func mismatchedExitRows(doc string) []exitRowMismatch {
	var bad []exitRowMismatch
	for _, line := range strings.Split(doc, "\n") {
		m := readmeRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		code, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if kindNames[Kind(code)] != m[2] {
			bad = append(bad, exitRowMismatch{code: code, name: m[2]})
		}
	}
	return bad
}

func TestUnknownExitHeadings(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int
	}{
		{name: "定義どおりの見出しだけ", doc: "### exit 2: a\n### exit 9: b\n", want: nil},
		{name: "定義に無い見出しがある", doc: "### exit 2: a\n### exit 10: b\n", want: []int{10}},
		{name: "地の文の exit 0 は拾わない", doc: "終わると exit 0 になる。\n### exit 5: a\n", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unknownExitHeadings(tt.doc)
			if len(got) != len(tt.want) {
				t.Fatalf("unknownExitHeadings() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("unknownExitHeadings()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMismatchedExitRows(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []exitRowMismatch
	}{
		{name: "定義どおりの行だけ", doc: "| 値 | 名前 | 意味 |\n|---|---|---|\n| 2 | Amazon authentication required | a |\n", want: nil},
		{name: "名前が食い違う", doc: "| 2 | wrong name | a |\n", want: []exitRowMismatch{{code: 2, name: "wrong name"}}},
		{name: "値が定義に無い", doc: "| 10 | something | a |\n", want: []exitRowMismatch{{code: 10, name: "something"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mismatchedExitRows(tt.doc)
			if len(got) != len(tt.want) {
				t.Fatalf("mismatchedExitRows() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("mismatchedExitRows()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDocsExitCodes(t *testing.T) {
	paths := []string{readmePath}
	docs, err := filepath.Glob(docsGlob)
	if err != nil {
		t.Fatalf("filepath.Glob(%q) = %v", docsGlob, err)
	}
	if len(docs) == 0 {
		t.Fatalf("filepath.Glob(%q) が 0 件", docsGlob)
	}
	paths = append(paths, docs...)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) = %v", path, err)
		}
		doc := string(data)
		if unknown := unknownExitHeadings(doc); len(unknown) != 0 {
			t.Errorf("%s に Kind に定義の無い「### exit N:」見出し = %v, want なし", path, unknown)
		}
		if bad := mismatchedExitRows(doc); len(bad) != 0 {
			t.Errorf("%s の表の行で値と名前が食い違う = %v, want なし", path, bad)
		}
	}
}
