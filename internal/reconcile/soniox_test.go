package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/store"
)

// createBody pushes p through the real management client against an httptest front door and returns the body of
// the provider create call.
func createBody(t *testing.T, p bifrost.Provider) []byte {
	t.Helper()
	var (
		mu   sync.Mutex
		body []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/login" {
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "session-1", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/providers" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = b
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	c := bifrost.New(srv.URL, "admin", "test-password", srv.Client())
	if err := c.CreateProvider(context.Background(), p); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return body
}

func jsonEq(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("decode body %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("body\n got %s\nwant %s", got, want)
	}
}

func TestSonioxProviderBody(t *testing.T) {
	got := createBody(t, sonioxProvider("http://127.0.0.1:18090", 30*time.Minute))
	// The plan's body plus the two fields the management client sends for every provider
	// (stream_idle_timeout_in_seconds, concurrency_and_buffer_size).
	jsonEq(t, got, `{"provider":"soniox",
	 "network_config":{"base_url":"http://127.0.0.1:18090","default_request_timeout_in_seconds":1860,"max_retries":0,
	   "stream_idle_timeout_in_seconds":300,"allow_private_network":true},
	 "concurrency_and_buffer_size":{"concurrency":1000,"buffer_size":5000},
	 "custom_provider_config":{"base_provider_type":"openai","is_key_less":false,"allowed_requests":{"transcription":true}}}`)
}

func TestSonioxProviderTimeoutFollowsDeadline(t *testing.T) {
	p := sonioxProvider("http://127.0.0.1:18090", 2*time.Hour)
	if p.RequestTimeout != 7260*time.Second {
		t.Errorf("RequestTimeout = %v, want 7260s", p.RequestTimeout)
	}
	var b struct {
		NetworkConfig struct {
			Timeout int `json:"default_request_timeout_in_seconds"`
		} `json:"network_config"`
	}
	if err := json.Unmarshal(createBody(t, p), &b); err != nil {
		t.Fatal(err)
	}
	if b.NetworkConfig.Timeout != 7260 {
		t.Errorf("default_request_timeout_in_seconds = %d, want 7260", b.NetworkConfig.Timeout)
	}
}

func sonioxStore(name, baseURL string) store.Provider {
	return store.Provider{Name: name, Kind: store.KindSoniox, BaseURL: baseURL}
}

func TestSonioxKindNameMustBeSoniox(t *testing.T) {
	h := newHarness(t)
	h.rec.SonioxURL = "http://127.0.0.1:18090"
	h.write(t, putAll([]store.Provider{sonioxStore("speech", "")}))
	if err := h.rec.Sync(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Sync = %v, want ErrInvalid", err)
	}
	if got := h.eng.Mutations(); len(got) != 0 {
		t.Errorf("calls %q", ops(got))
	}
}

func TestSonioxRejects(t *testing.T) {
	cases := []struct {
		name      string
		adapter   string
		provider  store.Provider
		wantInErr string
	}{
		{"base URL set", "http://127.0.0.1:18090", sonioxStore("soniox", "http://x.test"), "base URL"},
		{"type set", "http://127.0.0.1:18090", store.Provider{Name: "soniox", Kind: store.KindSoniox, Type: "openai"}, "type"},
		{"adapter not configured", "", sonioxStore("soniox", ""), "speech adapter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.rec.SonioxURL = tc.adapter
			h.write(t, putAll([]store.Provider{tc.provider}))
			err := h.rec.Sync(context.Background())
			if !errors.Is(err, ErrInvalid) || !bytes.Contains([]byte(err.Error()), []byte(tc.wantInErr)) {
				t.Fatalf("Sync = %v, want ErrInvalid mentioning %q", err, tc.wantInErr)
			}
		})
	}
}

func TestSonioxProviderReconciled(t *testing.T) {
	h := newHarness(t)
	h.rec.SonioxURL = "http://127.0.0.1:18090"
	h.rec.SonioxDeadline = time.Hour
	h.write(t, putAll([]store.Provider{sonioxStore(SonioxProviderName, "")}))
	h.sync(t)

	provs, _ := h.eng.ListProviders(context.Background())
	if len(provs) != 1 {
		t.Fatalf("providers %+v, want one", provs)
	}
	want := sonioxProvider("http://127.0.0.1:18090", time.Hour)
	if !reflect.DeepEqual(provs[0], want) {
		t.Errorf("provider %+v, want %+v", provs[0], want)
	}
	keys, _ := h.eng.ListKeys(context.Background(), SonioxProviderName)
	if len(keys) != 1 || keys[0].ID != "soniox-key" || !keys[0].Enabled {
		t.Errorf("keys %+v, want the store key soniox-key", keys)
	}

	// A second pass changes nothing.
	h.eng.Mutations()
	h.sync(t)
	if got := h.eng.Mutations(); len(got) != 0 {
		t.Errorf("second pass calls %q", ops(got))
	}
}

func TestSonioxDefaultDeadline(t *testing.T) {
	h := newHarness(t)
	h.rec.SonioxURL = "http://127.0.0.1:18090"
	h.write(t, putAll([]store.Provider{sonioxStore(SonioxProviderName, "")}))
	h.sync(t)
	provs, _ := h.eng.ListProviders(context.Background())
	if len(provs) != 1 || provs[0].RequestTimeout != 1860*time.Second {
		t.Errorf("providers %+v, want RequestTimeout 1860s", provs)
	}
}
