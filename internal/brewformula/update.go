package brewformula

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// versionLinePattern は行頭の空白 + version " で始まる行だけを見て、test do の version.to_s に当たらないようにする。
var versionLinePattern = regexp.MustCompile(`(?m)^[ \t]+version "([^"\n]*)"`)

var versionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type semver [3]int

// ShouldUpdate は tap の今の定義の本文と新しい Release の tag から、定義を書き換えるかを返す。
// tag の版が今の定義の version より新しく、tag が prerelease（- を含む）でないときだけ true。
// 今の定義から version を読めないとき、tag が v + major.minor.patch の形でないときは error。
func ShouldUpdate(current []byte, tag string) (bool, error) {
	if !tagPattern.MatchString(tag) {
		return false, fmt.Errorf("tag %q は v<major>.<minor>.<patch> の形ではない", tag)
	}
	cur, err := currentVersion(current)
	if err != nil {
		return false, err
	}
	if strings.Contains(tag, "-") {
		return false, nil
	}
	next, err := parseSemver(strings.TrimPrefix(tag, tagPrefix))
	if err != nil {
		return false, fmt.Errorf("tag %q: %w", tag, err)
	}
	return next.newerThan(cur), nil
}

func currentVersion(current []byte) (semver, error) {
	m := versionLinePattern.FindSubmatch(current)
	if m == nil {
		return semver{}, fmt.Errorf(`今の定義に version "<major>.<minor>.<patch>" の行が無い`)
	}
	v, err := parseSemver(string(m[1]))
	if err != nil {
		return semver{}, fmt.Errorf("今の定義の version: %w", err)
	}
	return v, nil
}

func parseSemver(s string) (semver, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return semver{}, fmt.Errorf("version %q は <major>.<minor>.<patch> の形ではない", s)
	}
	var v semver
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, fmt.Errorf("version %q の数値を読めない: %w", s, err)
		}
		v[i] = n
	}
	return v, nil
}

func (a semver) newerThan(b semver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
