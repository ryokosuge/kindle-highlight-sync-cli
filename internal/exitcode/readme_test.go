package exitcode

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	readmePath    = "../../README.md"
	readmeSection = "## exit code"
)

// readmeRow は「| 値 | 名前 | 意味 |」の表の 1 行。
var readmeRow = regexp.MustCompile(`^\|\s*(\d+)\s*\|\s*([^|]+?)\s*\|`)

// readmeTable は README の「## exit code」の節の表から、値と名前の対応を読む。
func readmeTable(t *testing.T) map[int]string {
	t.Helper()
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v", readmePath, err)
	}
	table := map[int]string{}
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.TrimSpace(line) == readmeSection
			continue
		}
		if !inSection {
			continue
		}
		m := readmeRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		code, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("strconv.Atoi(%q) = %v", m[1], err)
		}
		if _, dup := table[code]; dup {
			t.Errorf("README の %q の表に値 %d の行が複数ある", readmeSection, code)
		}
		table[code] = m[2]
	}
	return table
}

func TestReadmeExitCodeTable(t *testing.T) {
	table := readmeTable(t)
	kinds := []Kind{General, AmazonAuth, ConfigRequired, Notion, Browser, LocalState, SourceFormat, PartialFailure, AlreadyRunning}
	for _, kind := range kinds {
		t.Run(kind.String(), func(t *testing.T) {
			got, ok := table[int(kind)]
			if !ok {
				t.Fatalf("README の %q の表に値 %d の行が無い", readmeSection, int(kind))
			}
			if got != kind.String() {
				t.Errorf("README の値 %d の名前 = %q, want %q", int(kind), got, kind.String())
			}
		})
	}
	if len(table) != len(kinds) {
		t.Errorf("README の表の行数 = %v, want %v（1〜9 以外の行がある）: %v", len(table), len(kinds), table)
	}
}
