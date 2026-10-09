package source

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// AuthState は Amazon のセッションの状態（仕様書 §37）。
type AuthState int

const (
	AuthUnknown AuthState = iota // 判定不能（DOM が変わった可能性。認証切れとは断定しない）
	AuthOK                       // 認証済み
	AuthExpired                  // 認証切れ
)

const (
	authSignInHost        = "www.amazon.co.jp"
	authSelectorNotebook  = "#kp-notebook-library"
	authSelectorLoginPage = `form#ap_login_form, form[name="signIn"], input[name="email"], input[name="password"]`
)

var authSignInPathPrefixes = []string{"/ap/", "/ax/"}

// DetectAuth は最終 URL と HTML から Amazon の認証状態を判定する（仕様書 §37 / §18.9 (7)）。
// 最終 URL が sign-in（host が www.amazon.co.jp で path が /ap/ か /ax/ で始まる）なら HTML を見ずに AuthExpired。
// Notebook の必須要素（#kp-notebook-library）があれば AuthOK、無くて login page の印があれば AuthExpired、どちらも無ければ AuthUnknown。
// reason は最終 URL の host と path だけで組み、クエリ・fragment・認証情報・HTML の中身を入れない。
func DetectAuth(finalURL string, html []byte) (state AuthState, reason string) {
	u, err := url.Parse(finalURL)
	if err != nil {
		return AuthUnknown, "the final URL cannot be parsed"
	}

	hp := u.Host + u.Path
	if u.Host == authSignInHost {
		for _, prefix := range authSignInPathPrefixes {
			if strings.HasPrefix(u.Path, prefix) {
				return AuthExpired, fmt.Sprintf("redirected to the Amazon sign-in page %q", hp)
			}
		}
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return AuthUnknown, fmt.Sprintf("the HTML at %q cannot be parsed", hp)
	}

	if doc.Find(authSelectorNotebook).Length() > 0 {
		return AuthOK, fmt.Sprintf("the Kindle Notebook library is present at %q", hp)
	}
	if doc.Find(authSelectorLoginPage).Length() > 0 {
		return AuthExpired, fmt.Sprintf("a login form is present at %q without the Kindle Notebook library", hp)
	}

	return AuthUnknown, fmt.Sprintf("neither the Kindle Notebook library nor a login form is present at %q", hp)
}
