package main

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	repoURL         = "https://github.com/ryokosuge/kindle-highlight-sync-cli"
	bookTableHeader = "| Title | Kindle ID | Author | ASIN | Last Annotated At |"
)

var (
	headerBadgeRe = regexp.MustCompile(`\[!\[([^\]]+)\]\([^)]+\)\]\(([^)]+)\)`)
	headerQuoteRe = regexp.MustCompile(`^>\s*Location \S+( · Page \S+)? · khs:a-\S+$`)
)

// readmeHeader は README の最初の `## ` 行より前を返す。
func readmeHeader(readme string) string {
	lines := strings.Split(readme, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			return strings.Join(lines[:i], "\n")
		}
	}
	return readme
}

// headerTagline は H1 の次の、バッジでも空行でもない最初の行（売り文句）を返す。
func headerTagline(header string) string {
	lines := strings.Split(header, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		for _, l := range lines[i+1:] {
			if strings.TrimSpace(l) != "" {
				if strings.HasPrefix(l, "[") {
					return ""
				}
				return l
			}
		}
	}
	return ""
}

// headerBadgeLinks はバッジの名前（画像の alt の先頭語）からリンク先への対応を返す。
func headerBadgeLinks(header string) map[string]string {
	links := map[string]string{}
	for _, m := range headerBadgeRe.FindAllStringSubmatch(header, -1) {
		links[strings.TrimSuffix(strings.Fields(m[1])[0], ":")] = m[2]
	}
	return links
}

// headerHasFlowchart は冒頭に Kindle Notebook → CLI → Notion の ```mermaid の flowchart があるかを返す。
func headerHasFlowchart(header string) bool {
	_, rest, ok := strings.Cut(header, "```mermaid\n")
	if !ok {
		return false
	}
	body, _, ok := strings.Cut(rest, "```")
	if !ok || !strings.HasPrefix(strings.TrimSpace(body), "flowchart") {
		return false
	}
	return strings.Index(body, "Kindle Notebook") >= 0 &&
		strings.Index(body, "Kindle Notebook") < strings.Index(body, cliName) &&
		strings.Index(body, cliName) < strings.Index(body, "Notion")
}

// headerQuote は引用の塊 1 つについて、見本の形か（quoteOK）と、直後の段落があるか（hasNote）を持つ。
type headerQuote struct {
	quoteOK bool
	hasNote bool
}

func headerQuotes(header string) []headerQuote {
	lines := strings.Split(header, "\n")
	var out []headerQuote
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], ">") {
			continue
		}
		start := i
		for i+1 < len(lines) && strings.HasPrefix(lines[i+1], ">") {
			i++
		}
		block := lines[start : i+1]
		q := headerQuote{quoteOK: len(block) >= 2 && headerQuoteRe.MatchString(block[len(block)-1])}
		next := i + 1
		for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
			next++
		}
		q.hasNote = next < len(lines) && !strings.HasPrefix(lines[next], ">") && !strings.HasPrefix(lines[next], "#") && next > i+1
		out = append(out, q)
	}
	return out
}

func hasNoteAndNoNote(quotes []headerQuote) bool {
	var with, without bool
	for _, q := range quotes {
		if !q.quoteOK {
			return false
		}
		if q.hasNote {
			with = true
		} else {
			without = true
		}
	}
	return with && without
}

func headerTaglineAndBadgesOK(readme string) bool {
	header := readmeHeader(readme)
	links := headerBadgeLinks(header)
	return headerTagline(header) != "" && links["CI"] != "" && links["License"] != "" && links["Release"] != ""
}

func headerBadgeTargetsOK(readme string) bool {
	links := headerBadgeLinks(readmeHeader(readme))
	return links["CI"] == repoURL+"/actions/workflows/ci.yml" &&
		links["License"] == "LICENSE" &&
		links["Release"] == repoURL+"/releases"
}

func headerDiagramAndSamplesOK(readme string) bool {
	header := readmeHeader(readme)
	return headerHasFlowchart(header) &&
		strings.Contains(header, bookTableHeader+"\n| --- | --- | --- | --- | --- |\n| ") &&
		hasNoteAndNoNote(headerQuotes(header))
}

const headerValid = `# kindle-highlight-sync

売り文句。

[![CI](https://x/ci.svg)](` + repoURL + `/actions/workflows/ci.yml)
[![License: MIT](https://x/l.svg)](LICENSE)
[![Release](https://x/r.svg)](` + repoURL + `/releases)

` + "```mermaid\nflowchart LR\n    A[Kindle Notebook] --> B[kindle-highlight-sync]\n    B --> C[Notion Books Database]\n```" + `

` + bookTableHeader + `
| --- | --- | --- | --- | --- |
| 架空の本 | B0EXAMPLE01 | 架空 太郎 | B0EXAMPLE01 | 2026-01-31 |

> 引用 1。
> Location 120 · khs:a-aaaa

> 引用 2。
> Location 482 · Page 37 · khs:a-bbbb

Note の本文。

## インストール
`

func TestReadmeHeader(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(headerValid, old, new, 1) }
	tests := []struct {
		name  string
		check func(string) bool
		input string
		want  bool
	}{
		{name: "売り文句と 3 バッジが揃う", check: headerTaglineAndBadgesOK, input: headerValid, want: true},
		{name: "売り文句が無い", check: headerTaglineAndBadgesOK, input: replace("売り文句。\n\n", ""), want: false},
		{name: "CI バッジが無い", check: headerTaglineAndBadgesOK, input: replace("[![CI](https://x/ci.svg)]("+repoURL+"/actions/workflows/ci.yml)\n", ""), want: false},
		{name: "License バッジが無い", check: headerTaglineAndBadgesOK, input: replace("[![License: MIT](https://x/l.svg)](LICENSE)\n", ""), want: false},
		{name: "Release バッジが無い", check: headerTaglineAndBadgesOK, input: replace("[![Release](https://x/r.svg)]("+repoURL+"/releases)\n", ""), want: false},
		{name: "売り文句とバッジが全部 ## より後ろにある", check: headerTaglineAndBadgesOK, input: "# kindle-highlight-sync\n\n## 節\n売り文句。\n[![CI](a)](b)\n[![License](a)](b)\n[![Release](a)](b)\n", want: false},
		{name: "3 バッジのリンク先が正しい", check: headerBadgeTargetsOK, input: headerValid, want: true},
		{name: "CI のリンク先が違う", check: headerBadgeTargetsOK, input: replace("/actions/workflows/ci.yml)", "/actions)"), want: false},
		{name: "License のリンク先が違う", check: headerBadgeTargetsOK, input: replace("(LICENSE)", "(COPYING)"), want: false},
		{name: "Release のリンク先が違う", check: headerBadgeTargetsOK, input: replace("/releases)", "/tags)"), want: false},
		{name: "図と見本が揃う", check: headerDiagramAndSamplesOK, input: headerValid, want: true},
		{name: "mermaid の図が無い", check: headerDiagramAndSamplesOK, input: replace("```mermaid", "```text"), want: false},
		{name: "図が flowchart でない", check: headerDiagramAndSamplesOK, input: replace("flowchart LR", "sequenceDiagram"), want: false},
		{name: "図に Notion が無い", check: headerDiagramAndSamplesOK, input: replace("[Notion Books Database]", "[保存先]"), want: false},
		{name: "Book の表の列の並びが違う", check: headerDiagramAndSamplesOK, input: replace("| Title | Kindle ID |", "| Kindle ID | Title |"), want: false},
		{name: "Book の表が無い", check: headerDiagramAndSamplesOK, input: replace(bookTableHeader, ""), want: false},
		{name: "Highlight の引用が無い", check: headerDiagramAndSamplesOK, input: replace("> 引用 1。\n> Location 120 · khs:a-aaaa\n\n> 引用 2。\n> Location 482 · Page 37 · khs:a-bbbb\n\nNote の本文。\n\n", ""), want: false},
		{name: "Note 付きの Highlight が無い", check: headerDiagramAndSamplesOK, input: replace("Note の本文。\n\n", ""), want: false},
		{name: "Note の無い Highlight が無い", check: headerDiagramAndSamplesOK, input: replace("> 引用 1。\n> Location 120 · khs:a-aaaa\n\n", ""), want: false},
		{name: "Highlight の引用に Sync Marker が無い", check: headerDiagramAndSamplesOK, input: replace(" · khs:a-aaaa", ""), want: false},
		{name: "図と見本が最初の ## より後ろにある", check: headerDiagramAndSamplesOK, input: "# kindle-highlight-sync\n\n## 節\n" + readmeHeader(headerValid), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.check(tt.input); got != tt.want {
				t.Errorf("check(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}

	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", readmePath, err)
	}
	readme := string(b)
	t.Run("現行の README の売り文句と 3 バッジ", func(t *testing.T) {
		if got := headerTaglineAndBadgesOK(readme); !got {
			t.Errorf("headerTaglineAndBadgesOK(README) = %v, want true", got)
		}
	})
	t.Run("現行の README のバッジのリンク先", func(t *testing.T) {
		got := headerBadgeLinks(readmeHeader(readme))
		want := map[string]string{"CI": repoURL + "/actions/workflows/ci.yml", "License": "LICENSE", "Release": repoURL + "/releases"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("headerBadgeLinks(README) = %v, want %v", got, want)
		}
	})
	t.Run("現行の README の図と見本", func(t *testing.T) {
		if got := headerDiagramAndSamplesOK(readme); !got {
			t.Errorf("headerDiagramAndSamplesOK(README) = %v, want true", got)
		}
	})
}
