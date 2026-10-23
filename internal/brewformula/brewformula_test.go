package brewformula

import (
	"bytes"
	"strings"
	"testing"
)

const (
	shaARM64 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaAMD64 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	nameARM  = "kindle-highlight-sync_v1.2.3_darwin_arm64.tar.gz"
	nameAMD  = "kindle-highlight-sync_v1.2.3_darwin_amd64.tar.gz"
	otherSum = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func sumLine(sha, name string) string { return sha + "  " + name + "\n" }

func TestRender正常系(t *testing.T) {
	checksums := sumLine(shaARM64, nameARM) + sumLine(shaAMD64, nameAMD) +
		sumLine(otherSum, "kindle-highlight-sync_v1.2.3_linux_amd64.tar.gz")
	out, err := Render("v1.2.3", []byte(checksums))
	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	base := "https://github.com/ryokosuge/kindle-highlight-sync-cli/releases/download/v1.2.3/"
	for _, want := range []string{
		`version "1.2.3"`,
		`url "` + base + nameARM + `"`,
		`sha256 "` + shaARM64 + `"`,
		`url "` + base + nameAMD + `"`,
		`sha256 "` + shaAMD64 + `"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("Render() に %q が無い:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), otherSum) {
		t.Errorf("Render() に対象外の asset の sha256 が入っている:\n%s", out)
	}
}

func TestRender異常系(t *testing.T) {
	tests := []struct {
		name      string
		tag       string
		checksums string
		wantInErr string
	}{
		{"arm64 の行が無い", "v1.2.3", sumLine(shaAMD64, nameAMD), nameARM},
		{"amd64 の行が無い", "v1.2.3", sumLine(shaARM64, nameARM), nameAMD},
		{"両方の行が無い", "v1.2.3", sumLine(otherSum, "other.tar.gz"), nameARM},
		{"空入力", "v1.2.3", "", nameAMD},
		{"別の版の asset だけがある", "v1.2.3",
			sumLine(shaARM64, strings.ReplaceAll(nameARM, "v1.2.3", "v1.2.4")) +
				sumLine(shaAMD64, strings.ReplaceAll(nameAMD, "v1.2.3", "v1.2.4")), nameARM},
		{"sha256 が 64 桁でない", "v1.2.3", sumLine("abc", nameARM) + sumLine(shaAMD64, nameAMD), nameARM},
		{"sha256 に大文字がある", "v1.2.3", sumLine(strings.ToUpper(shaARM64), nameARM) + sumLine(shaAMD64, nameAMD), nameARM},
		{"同じ asset の行が 2 つある", "v1.2.3", sumLine(shaARM64, nameARM) + sumLine(shaARM64, nameARM) + sumLine(shaAMD64, nameAMD), nameARM},
		{"tag が v で始まらない", "1.2.3", sumLine(shaARM64, nameARM) + sumLine(shaAMD64, nameAMD), "1.2.3"},
		{"tag に引用符とセミコロンを含む", `v1.2.3";system("id");#`, "", "system"},
		{"tag に空白を含む", "v1.2.3 x", "", "v1.2.3 x"},
		{"tag に改行を含む", "v1.2.3\nend", "", "end"},
		{"tag が数字.数字.数字の形でない", "v1.2", "", `"v1.2"`},
		{"tag の接尾辞に使えない文字を含む", "v1.2.3-rc_1", "", "rc_1"},
		{"tag が v だけ", "v", "", `"v"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Render(tt.tag, []byte(tt.checksums))
			if err == nil {
				t.Fatalf("Render() error = nil, want error")
			}
			if out != nil {
				t.Errorf("Render() = %q, want nil", out)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("Render() error = %q, want %q を含む", err, tt.wantInErr)
			}
		})
	}
}

func TestRender同じ入力と行順の入れ替えで同じ本文(t *testing.T) {
	a := sumLine(shaARM64, nameARM) + sumLine(shaAMD64, nameAMD) + sumLine(otherSum, "x.tar.gz")
	b := sumLine(otherSum, "x.tar.gz") + sumLine(shaAMD64, nameAMD) + sumLine(shaARM64, nameARM)
	first, err := Render("v1.2.3", []byte(a))
	if err != nil {
		t.Fatalf("Render(a) error = %v, want nil", err)
	}
	second, _ := Render("v1.2.3", []byte(a))
	if !bytes.Equal(first, second) {
		t.Errorf("同じ入力の 2 回の出力が違う:\n%s\n---\n%s", first, second)
	}
	reordered, err := Render("v1.2.3", []byte(b))
	if err != nil {
		t.Fatalf("Render(b) error = %v, want nil", err)
	}
	if !bytes.Equal(first, reordered) {
		t.Errorf("行順を入れ替えた出力が違う:\n%s\n---\n%s", first, reordered)
	}
}

func TestRenderpre_releaseのtagも通る(t *testing.T) {
	tag := "v1.2.3-rc.1"
	checksums := sumLine(shaARM64, strings.ReplaceAll(nameARM, "v1.2.3", tag)) +
		sumLine(shaAMD64, strings.ReplaceAll(nameAMD, "v1.2.3", tag))
	out, err := Render(tag, []byte(checksums))
	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if want := `version "1.2.3-rc.1"`; !strings.Contains(string(out), want) {
		t.Errorf("Render() に %q が無い:\n%s", want, out)
	}
}
