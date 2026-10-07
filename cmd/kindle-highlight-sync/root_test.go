package main

import (
	"bytes"
	"strings"
	"testing"
)

const wantShort = "Kindle の Highlight と Note を Notion Books Database に同期する"

func TestRootCmd(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "引数なし", args: []string{}},
		{name: "--help", args: []string{"--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(out.String(), wantShort) {
				t.Errorf("output does not contain Short description: %q", out.String())
			}
		})
	}
}
