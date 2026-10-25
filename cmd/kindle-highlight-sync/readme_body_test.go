package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	readmeBodyMaxLines  = 80
	readmeBodyLocalName = "./" + cliName
)

var (
	readmeBodyLinkRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	// readmeBodyDocs は README から案内する docs/ の 3 ページ。
	readmeBodyDocs = []string{"docs/notion-setup.md", "docs/commands.md", "docs/troubleshooting.md"}
)

// readmeBodyLineCount は末尾の改行を除いた行数を返す（エディタの行番号と同じ数え方）。
func readmeBodyLineCount(readme string) int {
	return len(strings.Split(strings.TrimSuffix(readme, "\n"), "\n"))
}

// readmeBodyRelativeLinks は Markdown のリンクのうち相対パスのものを、# 以降を除いて重複なく出現順に返す。
func readmeBodyRelativeLinks(readme string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range readmeBodyLinkRe.FindAllStringSubmatch(readme, -1) {
		link, _, _ := strings.Cut(m[1], "#")
		if link == "" || strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, link)
	}
	return out
}

// readmeBodyMissingDocs は readme が案内していない docs/ のページを返す。
func readmeBodyMissingDocs(readme string) []string {
	links := readmeBodyRelativeLinks(readme)
	var missing []string
	for _, doc := range readmeBodyDocs {
		found := false
		for _, l := range links {
			found = found || l == doc
		}
		if !found {
			missing = append(missing, doc)
		}
	}
	return missing
}

func TestReadmeBody(t *testing.T) {
	linksAll := "[a](docs/notion-setup.md) [b](docs/commands.md#login) [c](docs/troubleshooting.md)\n"

	t.Run("行数", func(t *testing.T) {
		tests := []struct {
			name   string
			readme string
			want   int
		}{
			{name: "末尾の改行を数えない", readme: "a\nb\n", want: 2},
			{name: "末尾の改行が無くても同じ", readme: "a\nb", want: 2},
			{name: "空行も数える", readme: "a\n\nb\n", want: 3},
			{name: "80 行", readme: strings.Repeat("x\n", 80), want: 80},
			{name: "81 行", readme: strings.Repeat("x\n", 81), want: 81},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := readmeBodyLineCount(tt.readme); got != tt.want {
					t.Errorf("readmeBodyLineCount() = %v, want %v", got, tt.want)
				}
			})
		}
	})

	t.Run("相対リンクの抜き出し", func(t *testing.T) {
		tests := []struct {
			name   string
			readme string
			want   []string
		}{
			{name: "相対パスだけを抜き出し # 以降を除く", readme: "[a](docs/commands.md#login) [b](https://example.com/x) [c](LICENSE) [d](#top)", want: []string{"docs/commands.md", "LICENSE"}},
			{name: "同じリンクは 1 つにまとめる", readme: "[a](LICENSE) [b](LICENSE)", want: []string{"LICENSE"}},
			{name: "リンクが無い", readme: "docs/commands.md", want: nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := readmeBodyRelativeLinks(tt.readme); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("readmeBodyRelativeLinks() = %v, want %v", got, tt.want)
				}
			})
		}
	})

	t.Run("docs への案内", func(t *testing.T) {
		tests := []struct {
			name   string
			readme string
			want   []string
		}{
			{name: "3 ページとも案内している", readme: linksAll},
			{name: "notion-setup.md が欠ける", readme: "[b](docs/commands.md) [c](docs/troubleshooting.md)", want: []string{"docs/notion-setup.md"}},
			{name: "commands.md が欠ける", readme: "[a](docs/notion-setup.md) [c](docs/troubleshooting.md)", want: []string{"docs/commands.md"}},
			{name: "troubleshooting.md が欠ける", readme: "[a](docs/notion-setup.md) [b](docs/commands.md)", want: []string{"docs/troubleshooting.md"}},
			{name: "リンクでなく文字だけ書いてある", readme: "docs/notion-setup.md docs/commands.md docs/troubleshooting.md", want: readmeBodyDocs},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := readmeBodyMissingDocs(tt.readme); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("readmeBodyMissingDocs() = %v, want %v", got, tt.want)
				}
			})
		}
	})

	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", readmePath, err)
	}
	readme := string(b)
	t.Run("現行の README の行数", func(t *testing.T) {
		if got := readmeBodyLineCount(readme); got > readmeBodyMaxLines {
			t.Errorf("readmeBodyLineCount(README) = %v, want <= %v", got, readmeBodyMaxLines)
		}
	})
	t.Run("現行の README に ./kindle-highlight-sync が無い", func(t *testing.T) {
		if got := strings.Count(readme, readmeBodyLocalName); got != 0 {
			t.Errorf("strings.Count(README, %q) = %v, want 0", readmeBodyLocalName, got)
		}
	})
	t.Run("現行の README の docs への案内", func(t *testing.T) {
		if got := readmeBodyMissingDocs(readme); len(got) != 0 {
			t.Errorf("readmeBodyMissingDocs(README) = %v, want []", got)
		}
	})
	t.Run("現行の README の相対リンク先が存在する", func(t *testing.T) {
		for _, link := range readmeBodyRelativeLinks(readme) {
			path := filepath.Join(filepath.Dir(readmePath), link)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("os.Stat(%q) error = %v (README のリンク %q)", path, err, link)
			}
		}
	})
}

func TestReadmeBodyDotSlash(t *testing.T) {
	tests := []struct {
		name   string
		readme string
		want   int
	}{
		{name: "./kindle-highlight-sync が 1 つ", readme: "`./kindle-highlight-sync sync`", want: 1},
		{name: "./kindle-highlight-sync が 2 か所", readme: "./kindle-highlight-sync login\n./kindle-highlight-sync init", want: 2},
		{name: "go build の引数は一致しない", readme: "go build ./cmd/kindle-highlight-sync", want: 0},
		{name: "設定ディレクトリは一致しない", readme: "~/.config/kindle-highlight-sync/", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Count(tt.readme, readmeBodyLocalName); got != tt.want {
				t.Errorf("strings.Count(%q, %q) = %v, want %v", tt.readme, readmeBodyLocalName, got, tt.want)
			}
		})
	}
}
