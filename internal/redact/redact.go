// Package redact masks secrets in strings before they go to stderr or logs.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

const redacted = "[REDACTED]"

var (
	headerPattern      = regexp.MustCompile(`(?i)(authorization|set-cookie|cookie)(["']?\s*:\s*)[^\r\n]*`)
	notionTokenPattern = regexp.MustCompile(`(ntn|secret)_[A-Za-z0-9_-]+`)
)

type Redactor struct{ secrets []string }

func New(secrets ...string) *Redactor {
	var kept []string
	for _, s := range secrets {
		if s != "" {
			kept = append(kept, s)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return len(kept[i]) > len(kept[j])
	})
	return &Redactor{secrets: kept}
}

func (r *Redactor) String(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	s = headerPattern.ReplaceAllString(s, "${1}${2}"+redacted)
	s = notionTokenPattern.ReplaceAllString(s, redacted)
	return s
}
