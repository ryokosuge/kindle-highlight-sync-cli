package main

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const readmePath = "../../README.md"

var (
	readmeSubcommandRe = regexp.MustCompile("kindle-highlight-sync[ \t]+([a-z][a-z-]*)")
	readmeFlagRe       = regexp.MustCompile(`--[a-z][a-z-]*`)
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

// undefinedInReadme は readme に出るサブコマンドとフラグのうち、定義に無いものを返す。
func undefinedInReadme(readme string, subcommands, flags map[string]bool) (badSubcommands, badFlags []string) {
	for _, s := range uniqueMatches(readmeSubcommandRe, readme, 1) {
		if !subcommands[s] {
			badSubcommands = append(badSubcommands, s)
		}
	}
	for _, f := range uniqueMatches(readmeFlagRe, readme, 0) {
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
		{name: "見出しの直後の語はサブコマンドと見なさない", readme: "# kindle-highlight-sync\n\nintroduction"},
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

	t.Run("現行の README", func(t *testing.T) {
		b, err := os.ReadFile(readmePath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", readmePath, err)
		}
		gotSub, gotFlag := undefinedInReadme(string(b), subcommands, flags)
		if len(gotSub) > 0 {
			t.Errorf("%s の未定義のサブコマンド = %v, want none（定義: sync / login / init。README か CLI を直す）", readmePath, gotSub)
		}
		if len(gotFlag) > 0 {
			t.Errorf("%s の未定義のフラグ = %v, want none（root とサブコマンドの Flags に定義があるか確認する）", readmePath, gotFlag)
		}
	})
}
