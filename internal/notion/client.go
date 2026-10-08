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

type Client struct {
	token    string
	baseURL  string
	http     *http.Client
	redactor *redact.Redactor
}

type Option func(*Client)

// WithBaseURL は要求先を差し替える（テストの httptest サーバー向け）。
func WithBaseURL(u string) Option {
	return func(c *Client) {
		c.baseURL = u
	}
}

func New(token string, opts ...Option) *Client {
	c := &Client{
		token:    token,
		baseURL:  defaultBaseURL,
		http:     &http.Client{},
		redactor: redact.New(token),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return exitcode.New(exitcode.Notion, fmt.Errorf("notion: GET %s: %w", path, err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return exitcode.New(exitcode.Notion, fmt.Errorf("notion: GET %s: %w", path, err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return exitcode.New(exitcode.Notion, fmt.Errorf("notion: GET %s: %w", path, err))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiError struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		message := fmt.Sprintf("notion: GET %s: HTTP %d", path, resp.StatusCode)
		if json.Unmarshal(body, &apiError) == nil && apiError.Code != "" {
			message += " " + apiError.Code
			if apiError.Message != "" {
				message += ": " + apiError.Message
			}
		}
		return exitcode.New(exitcode.Notion, errors.New(c.redactor.String(message)))
	}
	if err := json.Unmarshal(body, v); err != nil {
		return exitcode.New(exitcode.Notion, errors.New("notion: GET "+path+": invalid response JSON"))
	}
	return nil
}

func invalidResponse(path, format string, args ...any) error {
	messageArgs := append([]any{path}, args...)
	return exitcode.New(exitcode.Notion, fmt.Errorf("notion: GET %s: invalid response: "+format, messageArgs...))
}
