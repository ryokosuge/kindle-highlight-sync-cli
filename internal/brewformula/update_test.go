package brewformula

import "testing"

func formulaWithVersion(version string) []byte {
	return []byte("class KindleHighlightSync < Formula\n  version \"" + version + "\"\n\n  test do\n    assert_match version.to_s, shell_output(\"x\")\n  end\nend\n")
}

func TestShouldUpdate(t *testing.T) {
	cur := formulaWithVersion("1.2.3")
	tests := []struct {
		name    string
		current []byte
		tag     string
		want    bool
		wantErr bool
	}{
		{"同じ版は更新しない", cur, "v1.2.3", false, false},
		{"patch が小さい版は更新しない", cur, "v1.2.2", false, false},
		{"minor が小さい版は更新しない", cur, "v1.1.9", false, false},
		{"major が小さい版は更新しない", cur, "v0.9.0", false, false},
		{"patch が大きい版は更新する", cur, "v1.2.4", true, false},
		{"minor が大きい版は更新する", cur, "v1.3.0", true, false},
		{"major が大きい版は更新する", cur, "v2.0.0", true, false},
		{"1.10.0 は 1.9.0 より新しい", formulaWithVersion("1.9.0"), "v1.10.0", true, false},
		{"1.9.0 は 1.10.0 より古い", formulaWithVersion("1.10.0"), "v1.9.0", false, false},
		{"prerelease の tag は新しく見えても更新しない", cur, "v1.3.0-rc.1", false, false},
		{"version の行が無い定義は error", []byte("class X < Formula\nend\n"), "v1.2.4", false, true},
		{"test do の version.to_s だけでは version を読めず error", []byte("  test do\n    assert_match version.to_s, x\n  end\n"), "v1.2.4", false, true},
		{"version が major.minor.patch の形でない定義は error", formulaWithVersion("1.2"), "v1.2.4", false, true},
		{"version に prerelease が付く定義は error", formulaWithVersion("1.2.3-rc.1"), "v1.2.4", false, true},
		{"v の無い tag は error", cur, "1.2.4", false, true},
		{"major.minor.patch でない tag は error", cur, "v1.2", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ShouldUpdate(tt.current, tt.tag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ShouldUpdate() の error = %v, want error = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ShouldUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}
