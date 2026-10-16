package notion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

func TestIsNotShared(t *testing.T) {
	calls := []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "Database の取得",
			call: func(c *Client) error {
				_, err := c.RetrieveDatabase(context.Background(), "db-1")
				return err
			},
		},
		{
			name: "Data Source の取得",
			call: func(c *Client) error {
				_, err := c.RetrieveDataSource(context.Background(), "ds-1")
				return err
			},
		},
	}
	responses := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "404 object_not_found は未接続", status: http.StatusNotFound, body: `{"code":"object_not_found","message":"x"}`, want: true},
		{name: "401 unauthorized は未接続ではない", status: http.StatusUnauthorized, body: `{"code":"unauthorized","message":"x"}`, want: false},
		{name: "403 restricted_resource は未接続ではない", status: http.StatusForbidden, body: `{"code":"restricted_resource","message":"x"}`, want: false},
		{name: "code が空の 404 は未接続ではない", status: http.StatusNotFound, body: `{}`, want: false},
	}
	for _, c := range calls {
		for _, r := range responses {
			t.Run(c.name+"/"+r.name, func(t *testing.T) {
				client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(r.status)
					_, _ = w.Write([]byte(r.body))
				})
				err := c.call(client)
				if err == nil {
					t.Fatal("err = nil, want Notion error")
				}
				if got := IsNotShared(err); got != r.want {
					t.Errorf("IsNotShared(err) = %v, want %v", got, r.want)
				}
			})
		}
	}
}

func TestIsNotShared_StatusErrorを含まないerror(t *testing.T) {
	notFound := &StatusError{Status: http.StatusNotFound, Code: codeObjectNotFound}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "StatusError を含まない error", err: errors.New("boom"), want: false},
		{name: "StatusError を fmt.Errorf で包んだもの", err: fmt.Errorf("wrap: %w", notFound), want: true},
		{name: "StatusError を exitcode.New で包んだもの", err: exitcode.New(exitcode.Notion, notFound), want: true},
		{name: "StatusError を含まない error を exitcode.New で包んだもの", err: exitcode.New(exitcode.Notion, errors.New("boom")), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotShared(tt.err); got != tt.want {
				t.Errorf("IsNotShared(err) = %v, want %v", got, tt.want)
			}
		})
	}
}
