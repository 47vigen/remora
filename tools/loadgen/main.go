// Command loadgen drives one remora /v1 endpoint at a fixed concurrency for a fixed time and prints one line of
// latency, time-to-first-token and throughput figures. make bench runs it; see tools/bench/run.sh.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/v1/chat/completions", "endpoint")
	key := flag.String("key", "", "bearer key")
	model := flag.String("model", "fast", "model")
	c := flag.Int("c", 10, "concurrency")
	d := flag.Duration("d", 10*time.Second, "duration")
	stream := flag.Bool("stream", false, "request a stream and measure time to the first data event")
	get := flag.Bool("get", false, "send GET without a body")
	bodyKB := flag.Int("body-kb", 0, "pad the user message to at least this many KiB")
	flag.Parse()

	tr := &http.Transport{
		Proxy: nil, MaxIdleConns: 4096, MaxIdleConnsPerHost: 4096, IdleConnTimeout: 90 * time.Second,
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, DisableCompression: true,
	}
	cl := &http.Client{Transport: tr}
	body := requestBody(*model, *stream, *bodyKB)

	var (
		mu        sync.Mutex
		tot, ttft []time.Duration
		ok        int
		failed    atomic.Int64
		failures  = map[string]int{} // status code, or "net" for a transport error
	)
	fail := func(why string) {
		failed.Add(1)
		mu.Lock()
		failures[why]++
		mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), *d)
	defer cancel()
	start := time.Now()
	var wg sync.WaitGroup
	for range *c {
		wg.Go(func() {
			for ctx.Err() == nil {
				t0 := time.Now()
				method, rd := http.MethodPost, io.Reader(bytes.NewReader(body))
				if *get {
					method, rd = http.MethodGet, nil
				}
				// The request outlives ctx on purpose: a request in flight at the deadline still counts.
				req, _ := http.NewRequestWithContext(context.Background(), method, *url, rd)
				req.Header.Set("Content-Type", "application/json")
				if *key != "" {
					req.Header.Set("Authorization", "Bearer "+*key)
				}
				resp, err := cl.Do(req)
				if err != nil {
					fail("net")
					time.Sleep(10 * time.Millisecond)
					continue
				}
				var first time.Duration
				if *stream && resp.StatusCode == http.StatusOK {
					_, err = countData(bufio.NewReaderSize(resp.Body, 64<<10), func() { first = time.Since(t0) })
				} else {
					_, err = io.Copy(io.Discard, resp.Body)
				}
				_ = resp.Body.Close()
				el := time.Since(t0)
				if resp.StatusCode != http.StatusOK {
					fail(strconv.Itoa(resp.StatusCode))
					continue
				}
				if err != nil {
					fail("body")
					continue
				}
				mu.Lock()
				ok++
				tot = append(tot, el)
				if first > 0 {
					ttft = append(ttft, first)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	wall := time.Since(start)
	slices.Sort(tot)
	slices.Sort(ttft)
	fmt.Printf("c=%d rps=%.1f p50=%s p90=%s p99=%s ttft_p50=%s ttft_p99=%s ok=%d err=%d%s\n",
		*c, float64(ok)/wall.Seconds(), ms(percentile(tot, .5)), ms(percentile(tot, .9)), ms(percentile(tot, .99)),
		ms(percentile(ttft, .5)), ms(percentile(ttft, .99)), ok, failed.Load(), failureList(failures))
	if ok == 0 {
		os.Exit(1)
	}
}

// percentile is the q-quantile of sorted, or 0 when it is empty.
func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q*float64(len(sorted))+0.5) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// countData reads an SSE body to its end and counts its data events, calling onFirst at the first one.
func countData(r *bufio.Reader, onFirst func()) (int, error) {
	n := 0
	for {
		line, err := r.ReadSlice('\n')
		if bytes.HasPrefix(line, []byte("data:")) {
			if n == 0 {
				onFirst()
			}
			n++
		}
		switch {
		case err == bufio.ErrBufferFull:
			// A line longer than the buffer: skip its rest.
			for err == bufio.ErrBufferFull {
				_, err = r.ReadSlice('\n')
			}
			if err != nil && err != io.EOF {
				return n, err
			}
		case err == io.EOF:
			return n, nil
		case err != nil:
			return n, err
		}
	}
}

// requestBody is a one-message chat request whose message is padded to at least kb KiB.
func requestBody(model string, stream bool, kb int) []byte {
	msg := "hello there, write a short answer"
	if pad := kb<<10 - len(msg); pad > 0 {
		msg += " " + strings.Repeat("lorem ipsum ", pad/12+1)
	}
	b, _ := json.Marshal(map[string]any{
		"model": model, "stream": stream,
		"messages": []map[string]string{{"role": "user", "content": msg}},
	})
	return b
}

// failureList is " (502:3,net:1)" sorted by cause, or "" when nothing failed.
func failureList(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ":" + strconv.Itoa(m[k])
	}
	return " (" + strings.Join(parts, ",") + ")"
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000) }
