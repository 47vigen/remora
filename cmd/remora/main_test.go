package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string // substring of stdout
		wantErr  string // substring of stderr
	}{
		{"version", []string{"version"}, 0, "remora dev", ""},
		{"no args", nil, 2, "", "usage: remora <serve|export|apply|import|mcp|version|healthcheck>"},
		{"unknown", []string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
		{"apply help", []string{"apply", "--help"}, 0, "usage: remora apply -f FILE", ""},
		{"export help", []string{"export", "-h"}, 0, "usage: remora export", ""},
		{"apply without a file", []string{"apply"}, 1, "", "usage: remora apply"},
		{"apply without credentials", []string{"apply", "-f", "../../test/e2e/testdata/remora.yaml"}, 1, "", "REMORA_TOKEN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			getenv := func(string) string { return "" }
			code := run(context.Background(), tt.args, getenv, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("code = %d, want %d (stderr %q)", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.wantOut)
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantErr)
			}
		})
	}
}
