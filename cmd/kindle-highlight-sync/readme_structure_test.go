package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	prerequisiteHeading = "## 前提"
	quickStartHeading   = "## Quick Start"
	quickStartMaxCmds   = 5
	notionSetupLink     = "docs/notion-setup.md"
)

// quickStartWant は Quick Start に入れる順のコマンド。
var quickStartWant = []string{
	"brew install ryokosuge/tap/kindle-highlight-sync",
	"kindle-highlight-sync login",
	"kindle-highlight-sync init",
	"kindle-highlight-sync sync",
}

// readmeSection は heading の行の次から、次の `## ` 行の手前までの行を返す。節が無ければ nil。
func readmeSection(readme, heading string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(readme, "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = line == heading
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return out
}

// prerequisiteItems は前提の節の箇条書きのうち、macOS・Google Chrome・Notion integration で始まるものの名前を節に現れる順に返す。
func prerequisiteItems(readme string) []string {
	var got []string
	for _, line := range readmeSection(readme, prerequisiteHeading) {
		item, ok := strings.CutPrefix(line, "- ")
		if !ok {
			continue
		}
		for _, name := range []string{"macOS", "Google Chrome", "Notion integration"} {
			if strings.HasPrefix(item, name) {
				got = append(got, name)
			}
		}
	}
	return got
}

// prerequisiteNotes は、Google Chrome が brew とは別に入れるものと書かれているか、Notion integration の項目から notion-setup.md を指しているかを返す。
func prerequisiteNotes(readme string) (chromeSeparate, notionLink bool) {
	for _, line := range readmeSection(readme, prerequisiteHeading) {
		if strings.HasPrefix(line, "- Google Chrome") {
			chromeSeparate = strings.Contains(line, "brew とは別に")
		}
		if strings.HasPrefix(line, "- Notion integration") {
			notionLink = strings.Contains(line, "("+notionSetupLink+")")
		}
	}
	return chromeSeparate, notionLink
}

// quickStartCommands は Quick Start の節のコードブロックの、空でない行を順に返す。
func quickStartCommands(readme string) []string {
	var cmds []string
	inFence := false
	for _, line := range readmeSection(readme, quickStartHeading) {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence && strings.TrimSpace(line) != "" {
			cmds = append(cmds, strings.TrimSpace(line))
		}
	}
	return cmds
}

// quickStartProblem は Quick Start のコマンドが 5 個以内で、入れる・login・init・sync の順かを確かめ、満たさない理由を返す。満たせば空。
func quickStartProblem(readme string) string {
	cmds := quickStartCommands(readme)
	if len(cmds) == 0 {
		return "Quick Start の節かそのコマンドが無い"
	}
	if len(cmds) > quickStartMaxCmds {
		return "コマンドが多すぎる"
	}
	if !reflect.DeepEqual(cmds, quickStartWant) {
		return "コマンドの内容か順が違う"
	}
	return ""
}

func TestReadmePrerequisites(t *testing.T) {
	const prereq = "## 前提\n\n- macOS（対象）\n- Google Chrome（brew とは別に入れる）\n- Notion integration（[docs/notion-setup.md](docs/notion-setup.md)）\n\n## 次\n"
	tests := []struct {
		name           string
		readme         string
		wantItems      []string
		wantChrome     bool
		wantNotionLink bool
	}{
		{name: "3 項目が揃う", readme: prereq, wantItems: []string{"macOS", "Google Chrome", "Notion integration"}, wantChrome: true, wantNotionLink: true},
		{name: "前提の節が無い", readme: "# t\n\n## 次\n- macOS\n"},
		{name: "macOS が欠ける", readme: strings.Replace(prereq, "- macOS（対象）\n", "", 1), wantItems: []string{"Google Chrome", "Notion integration"}, wantChrome: true, wantNotionLink: true},
		{name: "Google Chrome が欠ける", readme: strings.Replace(prereq, "- Google Chrome（brew とは別に入れる）\n", "", 1), wantItems: []string{"macOS", "Notion integration"}, wantNotionLink: true},
		{name: "Notion integration が欠ける", readme: strings.Replace(prereq, "- Notion integration（[docs/notion-setup.md](docs/notion-setup.md)）\n", "", 1), wantItems: []string{"macOS", "Google Chrome"}, wantChrome: true},
		{name: "Chrome が brew とは別と書かれていない", readme: strings.Replace(prereq, "brew とは別に入れる", "入れておく", 1), wantItems: []string{"macOS", "Google Chrome", "Notion integration"}, wantNotionLink: true},
		{name: "notion-setup.md への案内が無い", readme: strings.Replace(prereq, "[docs/notion-setup.md](docs/notion-setup.md)", "token", 1), wantItems: []string{"macOS", "Google Chrome", "Notion integration"}, wantChrome: true},
		{name: "3 項目が次の節にある", readme: "## 前提\n\n## 次\n- macOS\n- Google Chrome\n- Notion integration\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prerequisiteItems(tt.readme); !reflect.DeepEqual(got, tt.wantItems) {
				t.Errorf("prerequisiteItems() = %v, want %v", got, tt.wantItems)
			}
			chrome, link := prerequisiteNotes(tt.readme)
			if chrome != tt.wantChrome {
				t.Errorf("prerequisiteNotes() chromeSeparate = %v, want %v", chrome, tt.wantChrome)
			}
			if link != tt.wantNotionLink {
				t.Errorf("prerequisiteNotes() notionLink = %v, want %v", link, tt.wantNotionLink)
			}
		})
	}

	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", readmePath, err)
	}
	t.Run("現行の README の前提", func(t *testing.T) {
		want := []string{"macOS", "Google Chrome", "Notion integration"}
		if got := prerequisiteItems(string(b)); !reflect.DeepEqual(got, want) {
			t.Errorf("prerequisiteItems(README) = %v, want %v", got, want)
		}
		chrome, link := prerequisiteNotes(string(b))
		if !chrome || !link {
			t.Errorf("prerequisiteNotes(README) = (%v, %v), want (true, true)", chrome, link)
		}
	})
}

func TestReadmeQuickStart(t *testing.T) {
	const (
		brewCmd  = "brew install ryokosuge/tap/kindle-highlight-sync"
		loginCmd = "kindle-highlight-sync login"
		initCmd  = "kindle-highlight-sync init"
		syncCmd  = "kindle-highlight-sync sync"
	)
	quick := func(cmds ...string) string {
		return "## Quick Start\n\n```sh\n" + strings.Join(cmds, "\n") + "\n```\n\n## 次\n"
	}
	tests := []struct {
		name        string
		readme      string
		wantProblem bool
	}{
		{name: "4 コマンドが順に並ぶ", readme: quick(brewCmd, loginCmd, initCmd, syncCmd)},
		{name: "Quick Start の節が無い", readme: "## 前提\n\n```sh\n" + brewCmd + "\n```\n", wantProblem: true},
		{name: "コマンドが 6 つ以上", readme: quick(brewCmd, loginCmd, initCmd, syncCmd, syncCmd, syncCmd), wantProblem: true},
		{name: "login と init の順が逆", readme: quick(brewCmd, initCmd, loginCmd, syncCmd), wantProblem: true},
		{name: "sync が欠ける", readme: quick(brewCmd, loginCmd, initCmd), wantProblem: true},
		{name: "brew install が欠ける", readme: quick(loginCmd, initCmd, syncCmd), wantProblem: true},
		{name: "コマンドが次の節にある", readme: "## Quick Start\n\n## 次\n\n```sh\n" + strings.Join([]string{brewCmd, loginCmd, initCmd, syncCmd}, "\n") + "\n```\n", wantProblem: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quickStartProblem(tt.readme); (got != "") != tt.wantProblem {
				t.Errorf("quickStartProblem() = %q, want problem = %v", got, tt.wantProblem)
			}
		})
	}

	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", readmePath, err)
	}
	t.Run("現行の README の Quick Start", func(t *testing.T) {
		if got := quickStartProblem(string(b)); got != "" {
			t.Errorf("quickStartProblem(README) = %q, want %q", got, "")
		}
	})
}
