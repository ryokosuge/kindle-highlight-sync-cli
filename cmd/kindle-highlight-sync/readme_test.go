package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	readmePath = "../../README.md"
	docsGlob   = "../../docs/*.md"
	cliName    = "kindle-highlight-sync"
)

var (
	inlineCodeRe       = regexp.MustCompile("`[^`\n]+`")
	readmeSubcommandRe = regexp.MustCompile("kindle-highlight-sync[ \t]+([a-z][a-z0-9_-]*)")
	readmeFlagRe       = regexp.MustCompile(`--[a-z][a-z0-9_-]*`)
)

// cobraAutoSubcommands は cobra が自動で足すコマンド。README に書いてよいが、root command の定義には含めない。
var cobraAutoSubcommands = []string{"help", "completion"}

// definedCommandsAndFlags は root command に登録されたサブコマンド名と、root とサブコマンドに定義された --<フラグ名> を集める。
func definedCommandsAndFlags(root *cobra.Command) (subcommands, flags map[string]bool) {
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	subcommands, flags = map[string]bool{}, map[string]bool{}
	collect := func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		c.Flags().VisitAll(func(f *pflag.Flag) { flags["--"+f.Name] = true })
	}
	collect(root)
	for _, c := range root.Commands() {
		subcommands[c.Name()] = true
		collect(c)
	}
	return subcommands, flags
}

func uniqueMatches(re *regexp.Regexp, text string, group int) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if !seen[m[group]] {
			seen[m[group]] = true
			out = append(out, m[group])
		}
	}
	sort.Strings(out)
	return out
}

// isCLICommandWord は、コードの先頭の語がこの CLI のコマンド（本体・サブコマンド）か、フラグそのものかを返す。
func isCLICommandWord(word string, subcommands map[string]bool) bool {
	return word == cliName || word == "./"+cliName || subcommands[word] || strings.HasPrefix(word, "-")
}

func isExternalCommand(code string, subcommands map[string]bool) bool {
	fields := strings.Fields(code)
	return len(fields) > 0 && !isCLICommandWord(fields[0], subcommands)
}

// stripExternalCommands は、先頭の語が CLI 以外のコマンドであるコードブロックの行とインラインコードを text から除く。
func stripExternalCommands(text string, subcommands map[string]bool) string {
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if isExternalCommand(line, subcommands) {
				lines[i] = ""
			}
			continue
		}
		lines[i] = inlineCodeRe.ReplaceAllStringFunc(line, func(span string) string {
			if isExternalCommand(strings.Trim(span, "`"), subcommands) {
				return ""
			}
			return span
		})
	}
	return strings.Join(lines, "\n")
}

// undefinedInReadme は readme に出るサブコマンドとフラグのうち、定義に無いものを返す。外部コマンドのフラグは照合しない。
func undefinedInReadme(readme string, subcommands, flags map[string]bool) (badSubcommands, badFlags []string) {
	for _, s := range uniqueMatches(readmeSubcommandRe, readme, 1) {
		if !subcommands[s] {
			badSubcommands = append(badSubcommands, s)
		}
	}
	for _, f := range uniqueMatches(readmeFlagRe, stripExternalCommands(readme, subcommands), 0) {
		if !flags[f] {
			badFlags = append(badFlags, f)
		}
	}
	return badSubcommands, badFlags
}

func TestReadmeMatchesCLI(t *testing.T) {
	subcommands, flags := definedCommandsAndFlags(newRootCmd())
	for _, name := range cobraAutoSubcommands {
		subcommands[name] = true
	}

	tests := []struct {
		name           string
		readme         string
		wantSubcommand []string
		wantFlag       []string
	}{
		{name: "存在するサブコマンドとフラグだけ", readme: "`kindle-highlight-sync sync --dry-run`\n`kindle-highlight-sync init --force`\n`kindle-highlight-sync login`\n`kindle-highlight-sync --help`"},
		{name: "cobra が足す help と completion", readme: "`kindle-highlight-sync help` と `kindle-highlight-sync completion bash`"},
		{name: "存在しないサブコマンド", readme: "```\nkindle-highlight-sync export\n```", wantSubcommand: []string{"export"}},
		{name: "存在しないフラグ", readme: "`kindle-highlight-sync sync --verbose`", wantFlag: []string{"--verbose"}},
		{name: "存在しないサブコマンドとフラグの両方", readme: "`kindle-highlight-sync push --remote`", wantSubcommand: []string{"push"}, wantFlag: []string{"--remote"}},
		{name: "数字入りのサブコマンドとフラグ", readme: "`kindle-highlight-sync sync2 --dry-run2`", wantSubcommand: []string{"sync2"}, wantFlag: []string{"--dry-run2"}},
		{name: "アンダースコア入りのサブコマンドとフラグ", readme: "`kindle-highlight-sync sync_all --dry_run`", wantSubcommand: []string{"sync_all"}, wantFlag: []string{"--dry_run"}},
		{name: "見出しの直後の語はサブコマンドと見なさない", readme: "# kindle-highlight-sync\n\nintroduction"},
		{name: "docs 由来の存在しないサブコマンド", readme: "# コマンド\n\n```sh\nkindle-highlight-sync export\n```", wantSubcommand: []string{"export"}},
		{name: "docs 由来の存在しないフラグ", readme: "```sh\nkindle-highlight-sync sync --verbose\n```", wantFlag: []string{"--verbose"}},
		{name: "地の文に単独で書いた存在しないフラグ", readme: "`--verbose` を付けると詳細を出す。", wantFlag: []string{"--verbose"}},
		{name: "地の文に単独で書いた存在するフラグ", readme: "`--dry-run` を付けると書き込まない。--yes も使える。"},
		{name: "コードブロックの外部コマンドのフラグは照合しない", readme: "```sh\nbrew install --cask google-chrome\ngo build -o kindle-highlight-sync ./cmd/kindle-highlight-sync\n```"},
		{name: "インラインコードの外部コマンドのフラグは照合しない", readme: "`brew install --cask google-chrome` で入れる。"},
		{name: "外部コマンドの行と CLI のコマンドの行が並ぶと CLI 側だけ照合する", readme: "```sh\nbrew install --cask google-chrome\n./kindle-highlight-sync sync --verbose\n```", wantFlag: []string{"--verbose"}},
		{name: "先頭語がサブコマンドだけのインラインコードは照合に残す", readme: "`init --bogus`", wantFlag: []string{"--bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSub, gotFlag := undefinedInReadme(tt.readme, subcommands, flags)
			if !reflect.DeepEqual(gotSub, tt.wantSubcommand) {
				t.Errorf("undefined subcommands = %v, want %v", gotSub, tt.wantSubcommand)
			}
			if !reflect.DeepEqual(gotFlag, tt.wantFlag) {
				t.Errorf("undefined flags = %v, want %v", gotFlag, tt.wantFlag)
			}
		})
	}

	docs, err := filepath.Glob(docsGlob)
	if err != nil {
		t.Fatalf("Glob(%q) error = %v", docsGlob, err)
	}
	if len(docs) == 0 {
		t.Fatalf("Glob(%q) = %v, want 1 件以上", docsGlob, docs)
	}
	for _, path := range append([]string{readmePath}, docs...) {
		t.Run("現行の "+path, func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%q) error = %v", path, err)
			}
			gotSub, gotFlag := undefinedInReadme(string(b), subcommands, flags)
			if len(gotSub) > 0 {
				t.Errorf("%s の未定義のサブコマンド = %v, want []", path, gotSub)
			}
			if len(gotFlag) > 0 {
				t.Errorf("%s の未定義のフラグ = %v, want []", path, gotFlag)
			}
		})
	}
}
