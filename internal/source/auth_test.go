package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAuth(t *testing.T) {
	notebook := readAuthFixture(t, "auth_notebook.html")
	tests := []struct {
		name       string
		url        string
		html       []byte
		wantState  AuthState
		wantReason string
	}{
		{
			name:       "サインイン URL",
			url:        "https://www.amazon.co.jp/ap/signin?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook",
			html:       notebook,
			wantState:  AuthExpired,
			wantReason: `redirected to the Amazon sign-in page "www.amazon.co.jp/ap/signin"`,
		},
		{
			name:       "MFA URL",
			url:        "https://www.amazon.co.jp/ap/mfa",
			html:       notebook,
			wantState:  AuthExpired,
			wantReason: `redirected to the Amazon sign-in page "www.amazon.co.jp/ap/mfa"`,
		},
		{
			name:       "CVF URL",
			url:        "https://www.amazon.co.jp/ap/cvf",
			html:       notebook,
			wantState:  AuthExpired,
			wantReason: `redirected to the Amazon sign-in page "www.amazon.co.jp/ap/cvf"`,
		},
		{
			name:       "クレーム検証 URL",
			url:        "https://www.amazon.co.jp/ax/claim/validate",
			html:       notebook,
			wantState:  AuthExpired,
			wantReason: `redirected to the Amazon sign-in page "www.amazon.co.jp/ax/claim/validate"`,
		},
		{
			name:       "異なるホストのサインイン風 URL",
			url:        "https://www.amazon.com/ap/signin",
			html:       readAuthFixture(t, "auth_unknown.html"),
			wantState:  AuthUnknown,
			wantReason: `neither the Kindle Notebook library nor a login form is present at "www.amazon.com/ap/signin"`,
		},
		{
			name:       "境界外のパス",
			url:        "https://www.amazon.co.jp/apx/foo",
			html:       readAuthFixture(t, "auth_unknown.html"),
			wantState:  AuthUnknown,
			wantReason: `neither the Kindle Notebook library nor a login form is present at "www.amazon.co.jp/apx/foo"`,
		},
		{
			name:       "Notebook に書籍がある",
			url:        "https://read.amazon.co.jp/notebook",
			html:       notebook,
			wantState:  AuthOK,
			wantReason: `the Kindle Notebook library is present at "read.amazon.co.jp/notebook"`,
		},
		{
			name:       "Notebook が空",
			url:        "https://read.amazon.co.jp/notebook",
			html:       readAuthFixture(t, "auth_notebook_empty.html"),
			wantState:  AuthOK,
			wantReason: `the Kindle Notebook library is present at "read.amazon.co.jp/notebook"`,
		},
		{
			name:       "サインインフォーム",
			url:        "https://read.amazon.co.jp/notebook",
			html:       readAuthFixture(t, "auth_signin.html"),
			wantState:  AuthExpired,
			wantReason: `a login form is present at "read.amazon.co.jp/notebook" without the Kindle Notebook library`,
		},
		{
			name:       "識別マーカーなし",
			url:        "https://read.amazon.co.jp/notebook",
			html:       readAuthFixture(t, "auth_unknown.html"),
			wantState:  AuthUnknown,
			wantReason: `neither the Kindle Notebook library nor a login form is present at "read.amazon.co.jp/notebook"`,
		},
		{
			name:       "ログインフォーム ID",
			url:        "https://read.amazon.co.jp/notebook",
			html:       []byte(`<form id="ap_login_form"></form>`),
			wantState:  AuthExpired,
			wantReason: `a login form is present at "read.amazon.co.jp/notebook" without the Kindle Notebook library`,
		},
		{
			name:       "ログインフォーム name",
			url:        "https://read.amazon.co.jp/notebook",
			html:       []byte(`<form name="signIn"></form>`),
			wantState:  AuthExpired,
			wantReason: `a login form is present at "read.amazon.co.jp/notebook" without the Kindle Notebook library`,
		},
		{
			name:       "メール入力欄",
			url:        "https://read.amazon.co.jp/notebook",
			html:       []byte(`<input name="email">`),
			wantState:  AuthExpired,
			wantReason: `a login form is present at "read.amazon.co.jp/notebook" without the Kindle Notebook library`,
		},
		{
			name:       "パスワード入力欄",
			url:        "https://read.amazon.co.jp/notebook",
			html:       []byte(`<input name="password">`),
			wantState:  AuthExpired,
			wantReason: `a login form is present at "read.amazon.co.jp/notebook" without the Kindle Notebook library`,
		},
		{
			name:       "Notebook がログインマーカーより優先",
			url:        "https://read.amazon.co.jp/notebook",
			html:       []byte(`<div id="kp-notebook-library"></div><input name="email">`),
			wantState:  AuthOK,
			wantReason: `the Kindle Notebook library is present at "read.amazon.co.jp/notebook"`,
		},
		{
			name:       "URL の解析失敗",
			url:        "://bad",
			html:       readAuthFixture(t, "auth_signin.html"),
			wantState:  AuthUnknown,
			wantReason: "the final URL cannot be parsed",
		},
		{
			name:       "HTML の解析失敗",
			url:        "https://read.amazon.co.jp/notebook",
			html:       []byte(strings.Repeat("<div>", 600)),
			wantState:  AuthUnknown,
			wantReason: `the HTML at "read.amazon.co.jp/notebook" cannot be parsed`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotReason := DetectAuth(tt.url, tt.html)
			if gotState != tt.wantState {
				t.Errorf("DetectAuth() state = %v, want %v", gotState, tt.wantState)
			}
			if gotReason != tt.wantReason {
				t.Errorf("DetectAuth() reason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestDetectAuthReason(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		html       []byte
		wantState  AuthState
		wantReason string
	}{
		{
			name:       "サインイン URL",
			url:        "https://khs-user:khs-pass@www.amazon.co.jp/ap/signin?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook&session-token=khs-secret-token#khs-secret-fragment",
			html:       readAuthFixture(t, "auth_notebook.html"),
			wantState:  AuthExpired,
			wantReason: `redirected to the Amazon sign-in page "www.amazon.co.jp/ap/signin"`,
		},
		{
			name:       "Notebook",
			url:        "https://khs-user:khs-pass@read.amazon.co.jp/notebook?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook&session-token=khs-secret-token#khs-secret-fragment",
			html:       readAuthFixture(t, "auth_notebook.html"),
			wantState:  AuthOK,
			wantReason: `the Kindle Notebook library is present at "read.amazon.co.jp/notebook"`,
		},
		{
			name:       "ログインフォーム",
			url:        "https://khs-user:khs-pass@read.amazon.co.jp/notebook?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook&session-token=khs-secret-token#khs-secret-fragment",
			html:       readAuthFixture(t, "auth_signin.html"),
			wantState:  AuthExpired,
			wantReason: `a login form is present at "read.amazon.co.jp/notebook" without the Kindle Notebook library`,
		},
		{
			name:       "識別マーカーなし",
			url:        "https://khs-user:khs-pass@read.amazon.co.jp/notebook?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook&session-token=khs-secret-token#khs-secret-fragment",
			html:       readAuthFixture(t, "auth_unknown.html"),
			wantState:  AuthUnknown,
			wantReason: `neither the Kindle Notebook library nor a login form is present at "read.amazon.co.jp/notebook"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotReason := DetectAuth(tt.url, tt.html)
			if gotState != tt.wantState {
				t.Errorf("DetectAuth() state = %v, want %v", gotState, tt.wantState)
			}
			if gotReason != tt.wantReason {
				t.Errorf("DetectAuth() reason = %q, want %q", gotReason, tt.wantReason)
			}
			for _, sensitive := range []string{"khs-user", "khs-pass", "khs-secret-token", "khs-secret-fragment", "?", "#"} {
				if strings.Contains(gotReason, sensitive) {
					t.Errorf("DetectAuth() reason contains %q: %q", sensitive, gotReason)
				}
			}
		})
	}
}

func readAuthFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
