package notion

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// Problem は 1 つの項目についての不足か型違い。Property が nil なら不足（名前の一致も型の合う候補も無い）。
type Problem struct {
	Field    Field
	Property *Property // 名前が既定名に一致したが型が合わない Property
	Want     string    // §45 の型（title / rich_text / date）
}

// MappingCheck は Data Source の Property を §45 と照らした結果。
type MappingCheck struct {
	Missing    []Problem // 必須項目の不足
	Mismatched []Problem // 必須項目の型違い
	Skipped    []Problem // 任意項目の型違い（対応させない）
}

// CheckMapping は Data Source の Property を §45 と照らす。型の合う候補があって既定が決まらないだけの項目は問題にしない。
func CheckMapping(props []Property) MappingCheck {
	var check MappingCheck

	for _, f := range fields {
		want := fieldTypes[f]
		hasMatchingName := false
		hasMatchingType := false

		for _, prop := range props {
			if prop.Type == want {
				hasMatchingType = true
			}
			if !matchesDefaultName(f, prop.Name) {
				continue
			}
			hasMatchingName = true
			if prop.Type == want {
				continue
			}

			property := prop
			problem := Problem{Field: f, Property: &property, Want: want}
			if f.Required() {
				check.Mismatched = append(check.Mismatched, problem)
			} else {
				check.Skipped = append(check.Skipped, problem)
			}
		}

		if f.Required() && !hasMatchingType && !hasMatchingName {
			check.Missing = append(check.Missing, Problem{Field: f, Want: want})
		}
	}

	return check
}

// Err は Missing / Mismatched があれば exitcode.ConfigRequired の error を、無ければ nil を返す。文面は全件を "; " で区切った 1 行。
func (c MappingCheck) Err() error {
	var parts []string
	for _, problem := range c.Missing {
		if name := problem.Field.DefaultName(); name != "" {
			parts = append(parts, fmt.Sprintf("Notion で Property「%s」を型 %s で足してください", name, problem.Want))
		} else {
			parts = append(parts, fmt.Sprintf("Notion で型 %s の Property を足してください（名前は問いません）", problem.Want))
		}
	}
	for _, problem := range c.Mismatched {
		property := problem.Property
		parts = append(parts, fmt.Sprintf("Notion で Property「%s」の型を %s から %s に直してください", escapeName(property.Name), property.Type, problem.Want))
	}
	if len(parts) == 0 {
		return nil
	}
	return exitcode.New(exitcode.ConfigRequired, errors.New(strings.Join(parts, "; ")))
}

// escapeName は Property 名の制御文字をエスケープして、文面が 1 行に収まるようにする。
func escapeName(name string) string {
	quoted := strconv.Quote(name)
	return quoted[1 : len(quoted)-1]
}

// SkippedLines は Skipped を 1 件 1 行の文面にして返す。
func (c MappingCheck) SkippedLines() []string {
	var lines []string
	for _, problem := range c.Skipped {
		property := problem.Property
		lines = append(lines, fmt.Sprintf("not mapping property %q to %s: type is %s, want %s", property.Name, problem.Field, property.Type, problem.Want))
	}
	return lines
}
