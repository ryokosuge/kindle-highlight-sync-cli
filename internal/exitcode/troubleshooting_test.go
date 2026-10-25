package exitcode

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const troubleshootingPath = "../../docs/troubleshooting.md"

// exitHeading は docs の「### exit N:」の見出し。
var exitHeading = regexp.MustCompile(`^###\s+exit\s+(\d+):`)

// exitHeadings は Markdown の本文から「### exit N:」見出しの N を拾う。
func exitHeadings(doc string) map[int]bool {
	headings := map[int]bool{}
	for _, line := range strings.Split(doc, "\n") {
		m := exitHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		code, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		headings[code] = true
	}
	return headings
}

// troubleshootingKinds は docs/troubleshooting.md に節が要る Kind。
var troubleshootingKinds = []Kind{AmazonAuth, ConfigRequired, Notion, Browser, AlreadyRunning}

// missingExitSections は doc に「### exit N:」の節が無い troubleshootingKinds の値を返す。
func missingExitSections(doc string) []int {
	headings := exitHeadings(doc)
	var missing []int
	for _, kind := range troubleshootingKinds {
		if !headings[int(kind)] {
			missing = append(missing, int(kind))
		}
	}
	return missing
}

func TestMissingExitSections(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int
	}{
		{name: "節が全部ある", doc: "### exit 2: a\n### exit 3: b\n### exit 4: c\n### exit 5: d\n### exit 9: e\n", want: nil},
		{name: "exit 3 が欠ける", doc: "### exit 2: a\n### exit 4: c\n### exit 5: d\n### exit 9: e\n", want: []int{3}},
		{name: "exit 9 が欠ける", doc: "### exit 2: a\n### exit 3: b\n### exit 4: c\n### exit 5: d\n", want: []int{9}},
		{name: "見出しが全部欠ける", doc: "## つまずいたとき\n", want: []int{2, 3, 4, 5, 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingExitSections(tt.doc)
			if len(got) != len(tt.want) {
				t.Fatalf("missingExitSections() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("missingExitSections()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTroubleshootingExitSections(t *testing.T) {
	data, err := os.ReadFile(troubleshootingPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v", troubleshootingPath, err)
	}
	if missing := missingExitSections(string(data)); len(missing) != 0 {
		t.Errorf("%s に「### exit N:」の節が無い値 = %v, want なし", troubleshootingPath, missing)
	}
}
