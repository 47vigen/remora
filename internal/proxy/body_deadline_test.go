package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/store"
)

type trickleReader struct{ firstRead int }

func (r *trickleReader) Read(p []byte) (int, error) {
	if r.firstRead == 0 {
		r.firstRead = len(p)
		p[0] = '{'
		return 1, nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestReadBodyPreallocation(t *testing.T) {
	r := &trickleReader{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", io.NopCloser(r))
	req.ContentLength = 100 << 20
	_, raw, _, err := extractModel(req)
	if !errors.Is(err, io.ErrUnexpectedEOF) || raw != nil {
		t.Fatalf("extractModel returned %v, %v; want nil body and read error", raw, err)
	}
	if r.firstRead > 1<<20 {
		t.Fatalf("first read buffer = %d bytes, want at most 1 MiB", r.firstRead)
	}
}

type deadlineWriter struct {
	http.ResponseWriter
	deadlines chan time.Time
}

func (w *deadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.deadlines <- deadline
	if !deadline.IsZero() {
		deadline = time.Now().Add(100 * time.Millisecond)
	}
	return http.NewResponseController(w.ResponseWriter).SetReadDeadline(deadline)
}

func TestDeadlineStalledBody(t *testing.T) {
	deadlines := make(chan time.Time, 2)
	p := &Proxy{log: slog.New(slog.DiscardHandler)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.forward(&deadlineWriter{ResponseWriter: w, deadlines: deadlines}, r, route{}, shapeOpenAI, store.ClientKey{})
	}))
	defer srv.Close()
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\n\r\n{", 100<<20); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want 400 or 408", resp.StatusCode)
	}
	if d := <-deadlines; d.IsZero() {
		t.Fatal("read deadline was not set")
	}
	if d := <-deadlines; !d.IsZero() {
		t.Fatal("read deadline was not cleared")
	}
}
