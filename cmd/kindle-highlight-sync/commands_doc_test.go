package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const commandsDocPath = "../../docs/commands.md"

// docSubcommands は docs/commands.md に節があるサブコマンド。
var docSubcommands = []string{"login", "init", "sync"}

// definedFlagsOf は root に登録されたサブコマンド name が自分で定義するフラグの --<名前> を返す。
// cobra が自動で足す --help と root の --version は含めない。
func definedFlagsOf(root *cobra.Command, name string) []string {
	for _, c := range root.Commands() {
		if c.Name() != name {
			continue
		}
		var flags []string
		c.Flags().VisitAll(func(f *pflag.Flag) { flags = append(flags, "--"+f.Name) })
		return flags
	}
	return nil
}

// commandSection は doc から `## <name>` の見出し行から次の `## ` 見出しの手前までを返す。
// fenced code（```）の中の行は見出しとして扱わない。節が無ければ ("", false) を返す。
func commandSection(doc, name string) (string, bool) {
	heading := "## " + name
	var b strings.Builder
	inSection, inFence := false, false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			if inSection {
				break
			}
			if strings.TrimRight(line, " \t") == heading {
				inSection = true
			}
		}
		if inSection {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String(), inSection
}

// missingFlagsInSection は flags のうち section に現れないものを返す。
func missingFlagsInSection(section string, flags []string) []string {
	var missing []string
	for _, f := range flags {
		if !strings.Contains(section, f) {
			missing = append(missing, f)
		}
	}
	return missing
}

func TestCommandSection(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		cmd  string
		want string
		ok   bool
	}{
		{name: "該当の節だけを取る", doc: "# page\n\n## login\n\nlogin の説明\n\n## init\n\ninit の説明\n", cmd: "login", want: "## login\n\nlogin の説明\n\n", ok: true},
		{name: "最後の節は文末まで", doc: "## init\n\ninit の説明\n\n## sync\n\nsync の説明\n", cmd: "sync", want: "## sync\n\nsync の説明\n\n", ok: true},
		{name: "無い節", doc: "## login\n\nlogin の説明\n", cmd: "init", want: "", ok: false},
		{name: "code fence の中の見出し風の行は見出しにしない", doc: "## login\n\n```\n## init\nフェンス内\n```\n\nlogin の続き\n\n## init\n\ninit の説明\n", cmd: "login", want: "## login\n\n```\n## init\nフェンス内\n```\n\nlogin の続き\n\n", ok: true},
		{name: "前方一致しない見出しは別の節", doc: "## init\n\ninit の説明\n\n## init-extra\n\n別の説明\n", cmd: "init", want: "## init\n\ninit の説明\n\n", ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := commandSection(tt.doc, tt.cmd)
			if ok != tt.ok {
				t.Errorf("commandSection() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("commandSection() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandsDocCoversFlags(t *testing.T) {
	doc := "## login\n\nlogin の説明\n\n## init\n\n`--force` `--database-id`\n\n## sync\n\n`--headed` `--dry-run`\n"
	tests := []struct {
		name  string
		cmd   string
		flags []string
		want  []string
	}{
		{name: "全部ある節", cmd: "init", flags: []string{"--force", "--database-id"}},
		{name: "一部が無い節", cmd: "init", flags: []string{"--force", "--yes"}, want: []string{"--yes"}},
		{name: "節が無いときは全部無い", cmd: "export", flags: []string{"--force"}, want: []string{"--force"}},
		{name: "別の節の同名フラグでは埋まらない", cmd: "login", flags: []string{"--yes"}, want: []string{"--yes"}},
		{name: "フラグが無いサブコマンド", cmd: "login", flags: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			section, _ := commandSection(doc, tt.cmd)
			if got := missingFlagsInSection(section, tt.flags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("missingFlagsInSection() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCommandsDoc(t *testing.T) {
	b, err := os.ReadFile(commandsDocPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", commandsDocPath, err)
	}
	doc := string(b)
	root := newRootCmd()
	for _, name := range docSubcommands {
		name := name
		t.Run(name+" の節に定義したフラグが全部ある", func(t *testing.T) {
			section, ok := commandSection(doc, name)
			if !ok {
				t.Fatalf("%s に ## %s の節が無い", commandsDocPath, name)
			}
			if got := missingFlagsInSection(section, definedFlagsOf(root, name)); len(got) > 0 {
				t.Errorf("%s の %s の節に無いフラグ = %v, want []", commandsDocPath, name, got)
			}
		})
	}
	t.Run("実行名が ./ 付きで書かれていない", func(t *testing.T) {
		if strings.Contains(doc, "./kindle-highlight-sync") {
			t.Errorf("%s に ./kindle-highlight-sync が現れる, want 現れない", commandsDocPath)
		}
	})
}
