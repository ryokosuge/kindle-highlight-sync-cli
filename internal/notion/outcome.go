package notion

import (
	"errors"
	"net/http"
	"time"
)

// OutcomeUnknown は err が、要求が Notion に反映されたか分からない失敗かを返す。
// 429 / 5xx（500 / 502 / 503 / 504 / 529）と、呼び出し元の ctx によらない HTTP のタイムアウトが該当する。
func OutcomeUnknown(err error) bool {
	var re *redactedError
	if !errors.As(err, &re) {
		return false
	}
	if re.timeout {
		return true
	}
	if re.status == nil {
		return false
	}
	switch re.status.Status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		statusServiceOverload:
		return true
	}
	return false
}

// RetryAfter は 429 の error が持つ Retry-After を返す。無ければ ok = false。
func RetryAfter(err error) (d time.Duration, ok bool) {
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusTooManyRequests || !se.hasRetryAfter {
		return 0, false
	}
	return se.retryAfter, true
}
