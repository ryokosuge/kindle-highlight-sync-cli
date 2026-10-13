// Package notion は Notion API（Notion-Version 2026-03-11）を呼ぶ薄いクライアント。
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/redact"
)

const (
	defaultBaseURL = "https://api.notion.com"
	notionVersion  = "2026-03-11"
	requestTimeout = 30 * time.Second

	// maxDurationSeconds は time.Duration に収まる最大の秒数。これを超える Retry-After は換算で溢れる。
	maxDurationSeconds = int(math.MaxInt64 / int64(time.Second))
)

// Client は Notion API を呼ぶクライアント。token は Authorization ヘッダにだけ使い、error には出さない。
type Client struct {
	token    string
	baseURL  string
	http     *http.Client
	redactor *redact.Redactor
	retry    retryPolicy
}

// redactedError は token を隠したメッセージと StatusError のみを公開する。原因は token を含みうるので
// Unwrap では返さず、呼び出し側の取り消し判定に要る errors.Is と As だけを委譲する。
type redactedError struct {
	message string
	cause   error
	status  *StatusError
	timeout bool
}

func (e *redactedError) Error() string { return e.message }

func (e *redactedError) As(target any) bool {
	status, ok := target.(**StatusError)
	if !ok || e.status == nil {
		return false
	}
	*status = e.status
	return true
}

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

// WithLogWriter は再試行の待機ログの出力先を差し替える。既定は os.Stderr。
func WithLogWriter(w io.Writer) Option {
	return func(c *Client) {
		c.retry.log = w
	}
}

func withRetrySleep(f func(context.Context, time.Duration) error) Option {
	return func(c *Client) {
		c.retry.sleep = f
	}
}

// WithHTTPTimeout は 1 回の HTTP 要求のタイムアウトを差し替える（テストで結果不明の失敗を短い時間で起こす用）。
func WithHTTPTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.http.Timeout = d
	}
}

func withHTTPTimeout(d time.Duration) Option { return WithHTTPTimeout(d) }

func withRetryJitter(f func() time.Duration) Option {
	return func(c *Client) {
		c.retry.jitter = f
	}
}

// New は Notion API クライアントを作る。
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: defaultBaseURL,
		http: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		redactor: redact.New(token),
		retry: retryPolicy{
			sleep:  sleepContext,
			jitter: defaultJitter,
			log:    os.Stderr,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// notionError は Client が返す error の唯一の出口。メッセージは必ず redactor を通す。
func (c *Client) notionError(method, path string, err error) error {
	return c.newNotionError(method, path, err, false)
}

func (c *Client) newNotionError(method, path string, err error, timeout bool) error {
	var status *StatusError
	var se *StatusError
	if errors.As(err, &se) {
		status = &StatusError{
			Status:        se.Status,
			Code:          c.redactor.String(se.Code),
			message:       c.redactor.String(se.message),
			retryAfter:    se.retryAfter,
			hasRetryAfter: se.hasRetryAfter,
		}
	}
	return exitcode.New(exitcode.Notion, &redactedError{
		message: c.redactor.String(fmt.Sprintf("notion: %s %s: %v", method, path, err)),
		cause:   err,
		status:  status,
		timeout: timeout,
	})
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	return c.do(ctx, http.MethodGet, path, nil, v)
}

// do は Notion への要求の送信と応答の読み取りを 1 か所にまとめる。body が nil なら本文を送らない。
// 429 / 529 は retryPolicy に従って再送する（§29 の Read / Query / Idempotent Property Update）。
func (c *Client) do(ctx context.Context, method, path string, body, v any) error {
	return c.request(ctx, method, path, body, v, true)
}

// doOnce は do と同じ送り方で要求を 1 回だけ送り、429 / 529 でも再送しない（§29 の Highlight Append）。
func (c *Client) doOnce(ctx context.Context, method, path string, body, v any) error {
	return c.request(ctx, method, path, body, v, false)
}

func (c *Client) request(ctx context.Context, method, path string, body, v any, retry bool) error {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return c.notionError(method, path, err)
		}
	}

	for n := 1; ; n++ {
		resp, respBody, err := c.send(ctx, method, path, encoded)
		if err != nil {
			return c.newNotionError(method, path, err, ctx.Err() == nil && isTimeout(err))
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			if err := json.Unmarshal(respBody, v); err != nil {
				return c.notionError(method, path, errors.New("invalid response JSON"))
			}
			return nil
		}

		statusErr := statusError(resp.StatusCode, respBody)
		if resp.StatusCode == http.StatusTooManyRequests {
			if secs, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok && secs <= maxDurationSeconds {
				statusErr.retryAfter, statusErr.hasRetryAfter = time.Duration(secs)*time.Second, true
			}
		}
		if !retry || !retryable(resp.StatusCode) {
			return c.notionError(method, path, statusErr)
		}
		if n > maxRetries {
			return c.notionError(method, path, fmt.Errorf("retry limit exceeded after %d retries: %w", maxRetries, statusErr))
		}
		secs, ok := parseRetryAfter(resp.Header.Get("Retry-After"))
		if ok && secs > int(maxRetryAfter/time.Second) {
			return c.notionError(method, path, fmt.Errorf("Retry-After %ds exceeds %ds limit: %w", secs, int(maxRetryAfter/time.Second), statusErr))
		}
		d := c.retry.waitFor(n, time.Duration(secs)*time.Second, ok)
		c.retry.logWait(resp.StatusCode, n, d)
		if err := c.retry.sleep(ctx, d); err != nil {
			return c.notionError(method, path, err)
		}
	}
}

func (c *Client) send(ctx context.Context, method, path string, body []byte) (*http.Response, []byte, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, nil, err
	}
	return resp, respBody, nil
}

// StatusError は Notion が 2xx 以外で返した HTTP status と Notion の code。Client の error から errors.As で取り出せる。
type StatusError struct {
	Status  int
	Code    string
	message string

	retryAfter    time.Duration
	hasRetryAfter bool
}

func (e *StatusError) Error() string {
	message := fmt.Sprintf("HTTP %d", e.Status)
	if e.Code != "" {
		message += " " + e.Code
		if e.message != "" {
			message += ": " + e.message
		}
	}
	return message
}

func statusError(status int, body []byte) *StatusError {
	var apiError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	result := &StatusError{Status: status}
	if json.Unmarshal(body, &apiError) == nil && apiError.Code != "" {
		result.Code = apiError.Code
		result.message = apiError.Message
	}
	return result
}

func (c *Client) invalidResponse(method, path, format string, args ...any) error {
	return c.notionError(method, path, fmt.Errorf("invalid response: "+format, args...))
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
