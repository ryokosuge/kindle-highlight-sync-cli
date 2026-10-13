package main

import (
	"bytes"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

var cellSeparator = regexp.MustCompile(" {2,}")

// planCells は writePlan の出力を行ごとのセルの列に分解する。
// tabwriter は桁数で揃えるので日本語タイトルは見た目がずれる。値の並びだけを比べる。
func planCells(t *testing.T, out string) [][]string {
	t.Helper()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("writePlan() output = %q, want trailing newline", out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			rows = append(rows, []string{})
			continue
		}
		rows = append(rows, cellSeparator.Split(line, -1))
	}
	return rows
}

func assertAppendCountSum(t *testing.T, cells [][]string) {
	t.Helper()
	sum := 0
	for i, row := range cells[1:] {
		if len(row) == 0 {
			break
		}
		if len(row) < 5 {
			t.Fatalf("Book row %d cells = %d, want at least 5", i+1, len(row))
		}
		if row[4] == "-" {
			continue
		}
		count, err := strconv.Atoi(row[4])
		if err != nil {
			t.Fatalf("strconv.Atoi(%q) error = %v, want nil", row[4], err)
		}
		sum += count
	}
	for _, row := range cells {
		if len(row) > 0 && row[0] == "Highlights to append" {
			if len(row) < 2 {
				t.Fatalf("Highlights to append summary cells = %d, want at least 2", len(row))
			}
			if row[1] != strconv.Itoa(sum) {
				t.Errorf("sum of Book rows' Highlights to append = %d, want %s (summary)", sum, row[1])
			}
			return
		}
	}
	t.Fatal(`summary row "Highlights to append" not found`)
}

type failingWriter struct {
	err error
}

func (w *failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWritePlan(t *testing.T) {
	tests := []struct {
		name string
		plan syncengine.Plan
		want [][]string
	}{
		{
			name: "4 種類の Match Status が出揃う",
			plan: syncengine.Plan{
				Books: []syncengine.BookPlan{
					{
						Book:               domain.Book{Title: "吾輩は猫である"},
						Match:              destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
						Action:             syncengine.ActionAppend,
						HighlightsToAppend: 2,
					},
					{
						Book:               domain.Book{Title: "Go Programming"},
						Match:              destination.BookMatch{Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-2"}},
						Action:             syncengine.ActionAdoptAndAppend,
						HighlightsToAppend: 1,
					},
					{
						Book:               domain.Book{Title: "新しい本"},
						Match:              destination.BookMatch{Status: destination.BookMatchNone},
						Action:             syncengine.ActionCreateAndAppend,
						HighlightsToAppend: 3,
					},
					{
						Book:   domain.Book{Title: "曖昧な本"},
						Match:  destination.BookMatch{Status: destination.BookMatchAmbiguous},
						Action: syncengine.ActionSkip,
					},
				},
				Summary: syncengine.Summary{
					ExactMatches:       1,
					AdoptableMatches:   1,
					NewBooks:           1,
					Ambiguous:          1,
					HighlightsToAppend: 6,
				},
			},
			want: [][]string{
				{"Kindle Book", "Match Status", "Matched Notion Page", "Action", "Highlights to append"},
				{"吾輩は猫である", "exact", "page-1", "append", "2"},
				{"Go Programming", "adoptable", "page-2", "adopt + append", "1"},
				{"新しい本", "none", "-", "create + append", "3"},
				{"曖昧な本", "ambiguous", "-", "skip", "-"},
				{},
				{"Exact matches", "1"},
				{"Adoptable matches", "1"},
				{"New books", "1"},
				{"Ambiguous", "1"},
				{"Highlights to append", "6"},
			},
		},
		{
			name: "0 冊なら summary はすべて 0",
			plan: syncengine.Plan{},
			want: [][]string{
				{"Kindle Book", "Match Status", "Matched Notion Page", "Action", "Highlights to append"},
				{},
				{"Exact matches", "0"},
				{"Adoptable matches", "0"},
				{"New books", "0"},
				{"Ambiguous", "0"},
				{"Highlights to append", "0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writePlan(&buf, tt.plan); err != nil {
				t.Fatalf("writePlan() error = %v", err)
			}
			got := planCells(t, buf.String())
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("planCells(writePlan()) = %v, want %v", got, tt.want)
			}
			assertAppendCountSum(t, got)
		})
	}
}

func TestWritePlanError(t *testing.T) {
	t.Run("writer が失敗したらそのエラーをそのまま返す", func(t *testing.T) {
		wantErr := errors.New("write failed")
		err := writePlan(&failingWriter{err: wantErr}, syncengine.Plan{})
		if err != wantErr {
			t.Errorf("writePlan() error = %v, want %v", err, wantErr)
		}
	})
}

// applyCells は writeApplyResult が出す件数の行。
func applyCells(created, adopted, appended, skipped, failed string) [][]string {
	return [][]string{{"Created", created}, {"Adopted", adopted}, {"Appended highlights", appended}, {"Skipped (ambiguous)", skipped}, {"Failed", failed}}
}

// recheckCells は applyCells の Appended highlights の次の行に、確かめ直した Book の数と送り直した Highlight の数の行を足す。
func recheckCells(cells [][]string, rechecked, resent string) [][]string {
	out := slices.Clone(cells[:3])
	out = append(out, []string{"Rechecked books", rechecked}, []string{"Resent highlights", resent})
	return append(out, cells[3:]...)
}

func TestWriteApplyResult(t *testing.T) {
	tests := []struct {
		name          string
		result        syncengine.ApplyResult
		writeErr      error      // nil 以外なら書き込み先がこの error で失敗する
		wantCells     [][]string // writeErr が nil のとき、出力が持つ行
		wantNoBookIDs bool       // 出力に Book ID（B0000 始まり）を含まない
	}{
		{
			name: "作成・Adopt・追記した Highlight・skip・失敗の件数だけを出し、Book ID は出さない",
			result: syncengine.ApplyResult{
				Created:  []string{"B000000001", "B000000002"},
				Adopted:  []string{"B000000003"},
				Exact:    []string{"B000000004"},
				Skipped:  []string{"B000000005"},
				Appended: 7,
				Failed:   []syncengine.FailedBook{{BookID: "B000000006", Kind: syncengine.FailureNotionWrite}, {BookID: "B000000007", Kind: syncengine.FailureNotionWrite}},
			},
			wantCells:     applyCells("2", "1", "7", "1", "2"),
			wantNoBookIDs: true,
		},
		{
			name:      "確かめ直した Book があれば、その数と送り直した Highlight の数を足して出す",
			result:    syncengine.ApplyResult{Appended: 3, Rechecked: 2, Resent: 5},
			wantCells: recheckCells(applyCells("0", "0", "3", "0", "0"), "2", "5"),
		},
		{
			name:     "書き込み先が失敗したら error を返す",
			writeErr: errors.New("boom"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.writeErr != nil {
				if err := writeApplyResult(&failingWriter{err: tt.writeErr}, tt.result); err == nil {
					t.Error("writeApplyResult() error = nil, want error")
				}
				return
			}
			var buf bytes.Buffer
			if err := writeApplyResult(&buf, tt.result); err != nil {
				t.Fatal(err)
			}
			if got := planCells(t, buf.String()); !reflect.DeepEqual(got, tt.wantCells) {
				t.Errorf("writeApplyResult() cells = %q, want %q", got, tt.wantCells)
			}
			if got := strings.Contains(buf.String(), "B0000"); tt.wantNoBookIDs && got {
				t.Errorf("strings.Contains(writeApplyResult(), %q) = %v, want false", "B0000", got)
			}
		})
	}
}

// withTargetCount は cells の末尾に writeTargetCount が出す「対象の数 of 全 Book の数」の行を足す。
func withTargetCount(cells [][]string, targets, total string) [][]string {
	return append(cells, []string{"Target books", targets + " of " + total})
}
