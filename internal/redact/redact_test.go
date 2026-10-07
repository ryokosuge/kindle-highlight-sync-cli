package redact

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name    string
		secrets []string
		in      string
		want    string
	}{
		{
			name: "Authorization Bearer",
			in:   "Authorization: Bearer abc.def-123",
			want: "Authorization: " + redacted,
		},
		{
			name: "小文字の authorization",
			in:   "authorization: bearer abc",
			want: "authorization: " + redacted,
		},
		{
			name: "Cookie",
			in:   "Cookie: session-id=123; at-main=xyz",
			want: "Cookie: " + redacted,
		},
		{
			name: "Set-Cookie",
			in:   "Set-Cookie: at-main=xyz; Path=/; Secure",
			want: "Set-Cookie: " + redacted,
		},
		{
			name: "小文字の cookie",
			in:   "cookie: a=b",
			want: "cookie: " + redacted,
		},
		{
			name: "リクエストのダンプ中のヘッダ",
			in:   "GET /notebook HTTP/1.1\r\nCookie: a=b\r\nAccept: */*\r\n",
			want: "GET /notebook HTTP/1.1\r\nCookie: " + redacted + "\r\nAccept: */*\r\n",
		},
		{
			name: "ntn_ token",
			in:   "notion: invalid token ntn_abc123XYZ for request",
			want: "notion: invalid token " + redacted + " for request",
		},
		{
			name: "secret_ token",
			in:   "notion: invalid token secret_abc123XYZ for request",
			want: "notion: invalid token " + redacted + " for request",
		},
		{
			name:    "渡した secret が文中の複数箇所",
			secrets: []string{"my-token-value"},
			in:      "first my-token-value then my-token-value.",
			want:    "first " + redacted + " then " + redacted + ".",
		},
		{
			name:    "渡した secret が正規表現の記号を含む",
			secrets: []string{"a.b*c+(d)"},
			in:      "value=a.b*c+(d) end",
			want:    "value=" + redacted + " end",
		},
		{
			name:    "渡した secret が正規表現の記号を含む（一致しない場合）",
			secrets: []string{"a.b*c+(d)"},
			in:      "value=aXbbc end",
			want:    "value=aXbbc end",
		},
		{
			name:    "一方が他方を含む secret",
			secrets: []string{"abc", "abcdef"},
			in:      "x abcdef y abc",
			want:    "x " + redacted + " y " + redacted,
		},
		{
			name:    "空文字の secret は無視",
			secrets: []string{""},
			in:      "no secrets here",
			want:    "no secrets here",
		},
		{
			name:    "secret を含まない文字列は変わらない",
			secrets: []string{"my-token-value"},
			in:      "sync: failed to fetch Book B000123: status 500",
			want:    "sync: failed to fetch Book B000123: status 500",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.secrets...).String(tt.in)
			if got != tt.want {
				t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
