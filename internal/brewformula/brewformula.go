// Package brewformula は Release の tag と checksums.txt から brew の Formula の本文を組み立てる。
package brewformula

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

const (
	repoURL     = "https://github.com/ryokosuge/kindle-highlight-sync-cli"
	assetPrefix = "kindle-highlight-sync"
	tagPrefix   = "v"
	archARM64   = "arm64"
	archAMD64   = "amd64"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// tagPattern は Formula の文字列リテラルに入る tag を、引用符や制御文字を含み得ない SemVer の形に絞る。
var tagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

var formulaTemplate = template.Must(template.New("formula").Parse(`class KindleHighlightSync < Formula
  desc "Sync Kindle highlights and notes to a Notion Books database"
  homepage "{{.Homepage}}"
  version "{{.Version}}"
  license "MIT"

  on_macos do
    on_arm do
      url "{{.ARM64.URL}}"
      sha256 "{{.ARM64.SHA256}}"
    end
    on_intel do
      url "{{.AMD64.URL}}"
      sha256 "{{.AMD64.SHA256}}"
    end
  end

  def install
    bin.install "kindle-highlight-sync"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kindle-highlight-sync --version")
  end
end
`))

type asset struct {
	URL    string
	SHA256 string
}

type formulaData struct {
	Homepage string
	Version  string
	ARM64    asset
	AMD64    asset
}

// Render は tag（例: v1.2.3）と checksums.txt の本文から Formula の本文を返す。
// 対象の asset は tag から組み立てた名前の完全一致で引く。
func Render(tag string, checksums []byte) ([]byte, error) {
	if !tagPattern.MatchString(tag) {
		return nil, fmt.Errorf("tag %q は %q + 数字.数字.数字（任意で -pre-release）の形（例: v1.2.3, v1.2.3-rc.1）でなければならない", tag, tagPrefix)
	}
	arm64Name := assetName(tag, archARM64)
	amd64Name := assetName(tag, archAMD64)
	sums, err := lookup(checksums, arm64Name, amd64Name)
	if err != nil {
		return nil, err
	}
	data := formulaData{
		Homepage: repoURL,
		Version:  strings.TrimPrefix(tag, tagPrefix),
		ARM64:    asset{URL: assetURL(tag, arm64Name), SHA256: sums[arm64Name]},
		AMD64:    asset{URL: assetURL(tag, amd64Name), SHA256: sums[amd64Name]},
	}
	var buf bytes.Buffer
	if err := formulaTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("brew の定義の本文を組み立てられない: %w", err)
	}
	return buf.Bytes(), nil
}

func assetName(tag, arch string) string {
	return fmt.Sprintf("%s_%s_darwin_%s.tar.gz", assetPrefix, tag, arch)
}

func assetURL(tag, name string) string {
	return fmt.Sprintf("%s/releases/download/%s/%s", repoURL, tag, name)
}

// lookup は checksums.txt（`<sha256>  <asset 名>` の行）から names の sha256 を返す。
// 対象外の行は読み飛ばす。
func lookup(checksums []byte, names ...string) (map[string]string, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	found := make(map[string]string, len(names))
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if !want[name] {
			continue
		}
		if _, dup := found[name]; dup {
			return nil, fmt.Errorf("checksums.txt に asset %s の行が 2 つ以上ある", name)
		}
		if !sha256Pattern.MatchString(fields[0]) {
			return nil, fmt.Errorf("checksums.txt の asset %s の sha256 が 64 桁の小文字 hex ではない: %q", name, fields[0])
		}
		found[name] = fields[0]
	}
	var missing []string
	for _, n := range names {
		if _, ok := found[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return nil, errors.New("checksums.txt に asset の行が無い: " + strings.Join(missing, ", "))
	}
	return found, nil
}
