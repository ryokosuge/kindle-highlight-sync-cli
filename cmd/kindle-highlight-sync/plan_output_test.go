package main

import (
	"bytes"
	"errors"
	"reflect"
	"regexp"
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
				{"Kindle Book", "Match Status", "Matched Notion Page", "Action"},
				{"吾輩は猫である", "exact", "page-1", "append"},
				{"Go Programming", "adoptable", "page-2", "adopt + append"},
				{"新しい本", "none", "-", "create + append"},
				{"曖昧な本", "ambiguous", "-", "skip"},
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
				{"Kindle Book", "Match Status", "Matched Notion Page", "Action"},
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
