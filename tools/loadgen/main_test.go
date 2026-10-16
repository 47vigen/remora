package main

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPercentiles(t *testing.T) {
	d := make([]time.Duration, 100)
	for i := range d {
		d[i] = time.Duration(i+1) * time.Millisecond
	}
	if got := percentile(d, 0.50); got != 50*time.Millisecond {
		t.Errorf("p50 = %v, want 50ms", got)
	}
	if got := percentile(d, 0.99); got != 99*time.Millisecond {
		t.Errorf("p99 = %v, want 99ms", got)
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("empty p50 = %v, want 0", got)
	}
}

func TestFirstDataEvent(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(": keepalive\n\ndata: {\"x\":1}\n\ndata: [DONE]\n\n"))
	n, err := countData(r, func() { t.Log("first") })
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("data events = %d, want 2", n)
	}
	var first int
	r = bufio.NewReader(strings.NewReader(": keepalive\n\ndata: a\n\n"))
	if _, err := countData(r, func() { first++ }); err != nil {
		t.Fatal(err)
	}
	if first != 1 {
		t.Fatalf("onFirst called %d times, want 1", first)
	}
}

func TestBodyPadding(t *testing.T) {
	b := requestBody("m", true, 64)
	var v struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.Model != "m" || !v.Stream || len(v.Messages) != 1 {
		t.Fatalf("body = %+v", v)
	}
	if len(b) < 64<<10 {
		t.Fatalf("len = %d, want ≥ 64 KiB", len(b))
	}
}

func TestFailureList(t *testing.T) {
	if got := failureList(nil); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := failureList(map[string]int{"net": 1, "502": 3}); got != " (502:3,net:1)" {
		t.Errorf("list = %q", got)
	}
}
