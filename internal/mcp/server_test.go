package mcp

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/47vigen/remora/internal/api/gen"
)

// readTools and writeTools are every tool New registers; Tasks 3–5 extend them.
var (
	readTools  = []string{"remora_overview", "remora_inspect", "remora_logs", "remora_stats", "remora_alias_compile", "remora_provider_test", "remora_config_export", "remora_config_plan"}
	writeTools = []string{"remora_config_apply", "remora_delete", "remora_account"}
)

func TestCheckCarriesRemoraMessage(t *testing.T) {
	e := &gen.Error{Error: gen.ErrorDetail{Message: "No alias fast."}}
	if _, err := check[gen.Alias](http.StatusNotFound, nil, e, nil); err == nil || err.Error() != "No alias fast. (HTTP 404)" {
		t.Errorf("err = %v", err)
	}
	if _, err := check[gen.Alias](http.StatusBadGateway, nil, nil, nil); err == nil || err.Error() != "HTTP 502" {
		t.Errorf("err = %v", err)
	}
	if a, err := check(http.StatusOK, &gen.Alias{Name: "fast"}, nil, nil); err != nil || a.Name != "fast" {
		t.Errorf("ok = %+v, %v", a, err)
	}
}

// seen is a handler that records the Authorization of each request and answers health.
type seen struct{ auth []string }

func (s *seen) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","engines":{}}`))
}

func TestInProcessForwardsCallerAuthorization(t *testing.T) {
	h := &seen{}
	c, err := InProcess(h)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.GetHealthWithResponse(WithAuthorization(context.Background(), "Bearer rmt_a"))
	if err != nil || r.JSON200 == nil {
		t.Fatalf("health = %+v, %v", r, err)
	}
	if !slices.Equal(h.auth, []string{"Bearer rmt_a"}) {
		t.Errorf("handler saw %q", h.auth)
	}
}

func TestInProcessSendsNoCredentialWithoutCaller(t *testing.T) {
	h := &seen{}
	c, err := InProcess(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetHealthWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.auth, []string{""}) {
		t.Errorf("handler saw %q", h.auth)
	}
}

func TestToolsAnnotated(t *testing.T) {
	cs := newFakeAPI(t).session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		switch {
		case a == nil:
			t.Errorf("%s has no annotations", tool.Name)
		case slices.Contains(readTools, tool.Name):
			if !a.ReadOnlyHint {
				t.Errorf("%s: readOnlyHint false", tool.Name)
			}
		case a.ReadOnlyHint || a.DestructiveHint == nil:
			t.Errorf("%s: write tool annotated %+v", tool.Name, a)
		}
	}
	want := slices.Concat(readTools, writeTools)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}
