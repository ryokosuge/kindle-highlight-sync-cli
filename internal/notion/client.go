// Package notion は Notion API（Notion-Version 2026-03-11）を呼ぶ薄いクライアント。
package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/redact"
)

const (
	defaultBaseURL = "https://api.notion.com"
	notionVersion  = "2026-03-11"
)

// Client は Notion API を呼ぶクライアント。token は Authorization ヘッダにだけ使い、error には出さない。
type Client struct {
	token    string
	baseURL  string
	http     *http.Client
	redactor *redact.Redactor
}

// redactedError は token を隠したメッセージだけを公開する。原因は token を含みうるので Unwrap では返さず、
// 呼び出し側の取り消し判定に要る context.Canceled / context.DeadlineExceeded の errors.Is だけを委譲する。
type redactedError struct {
	message string
	cause   error
}

func (e *redactedError) Error() string { return e.message }

func (e *redactedError) Is(target error) bool {
	return (target == context.Canceled || target == context.DeadlineExceeded) && errors.Is(e.cause, target)
}

// Option は Client の設定を表す。
type Option func(*Client)

// WithBaseURL は要求先を差し替える（テストの httptest サーバー向け）。
func WithBaseURL(u string) Option {
	return func(c *Client) {
		c.baseURL = u
	}
}

// New は Notion API クライアントを作る。
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: defaultBaseURL,
		http: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		redactor: redact.New(token),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// notionError は Client が返す error の唯一の出口。メッセージは必ず redactor を通す。
func (c *Client) notionError(path string, err error) error {
	return exitcode.New(exitcode.Notion, &redactedError{
		message: c.redactor.String(fmt.Sprintf("notion: GET %s: %v", path, err)),
		cause:   err,
	})
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return c.notionError(path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return c.notionError(path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.notionError(path, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiError struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		message := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if json.Unmarshal(body, &apiError) == nil && apiError.Code != "" {
			message += " " + apiError.Code
			if apiError.Message != "" {
				message += ": " + apiError.Message
			}
		}
		return c.notionError(path, errors.New(message))
	}
	if err := json.Unmarshal(body, v); err != nil {
		return c.notionError(path, errors.New("invalid response JSON"))
	}
	return nil
}

func (c *Client) invalidResponse(path, format string, args ...any) error {
	return c.notionError(path, fmt.Errorf("invalid response: "+format, args...))
}
