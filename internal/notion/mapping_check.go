package notion

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

type Problem struct {
	Field    Field
	Property *Property
	Want     string
}

type MappingCheck struct {
	Missing    []Problem
	Mismatched []Problem
	Skipped    []Problem
}

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

func (c MappingCheck) Err() error {
	var parts []string
	for _, problem := range c.Missing {
		parts = append(parts, fmt.Sprintf("missing property for %s: need a property of type %s", problem.Field, problem.Want))
	}
	for _, problem := range c.Mismatched {
		property := problem.Property
		parts = append(parts, fmt.Sprintf("property %q for %s has type %s, want %s", property.Name, problem.Field, property.Type, problem.Want))
	}
	if len(parts) == 0 {
		return nil
	}
	return exitcode.New(exitcode.ConfigRequired, errors.New(strings.Join(parts, "; ")))
}

func (c MappingCheck) SkippedLines() []string {
	var lines []string
	for _, problem := range c.Skipped {
		property := problem.Property
		lines = append(lines, fmt.Sprintf("not mapping property %q to %s: type is %s, want %s", property.Name, problem.Field, property.Type, problem.Want))
	}
	return lines
}
