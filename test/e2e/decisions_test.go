//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

func TestDecisions(t *testing.T) {
	resetFakes(t)
	quota := control.Step{
		Status: http.StatusTooManyRequests,
		Body:   `{"detail":{"error_type":"insufficient_quota","message":"free allowance used"}}`,
	}
	// One failure for each of the two calls below.
	script(t, "ts-a", control.RouteTypeSafeSystemOne, control.Script{Steps: []control.Step{quota, quota}})

	rep := doV1(t, http.MethodPost, "/v1/systemone", keyFull, map[string]any{
		"model": "jev",
		"state": "The parcel arrived two days late. " + persian,
		"questions": map[string]any{
			"late": map[string]any{"type": "noul", "instructions": "Did the parcel arrive late?"},
		},
	})
	wantStatus(t, rep, http.StatusOK)
	var native struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	decodeJSON(t, rep.Body, &native)
	if a, ok := native.Answers["late"]; !ok || a.Type != "noul" || a.Noul == nil || *a.Noul != control.DefaultNoul {
		t.Errorf("systemone body %s, want the fake's native noul answer for \"late\"", rep.Body)
	}
	wantHeader(t, rep, "X-Remora-Alias", "jev")
	wantHeader(t, rep, "X-Remora-Served-By", "ts-b/jev")

	rep = doV1(t, http.MethodPost, "/v1/decisions", keyFull, map[string]any{
		"model": "jev",
		"input": "The parcel arrived two days late. " + persian,
		"questions": []map[string]any{{
			"type": "predicate", "name": "late", "instructions": "Did the parcel arrive late?",
			"criteria": map[string]any{"true": "it arrived after the promised day", "false": "it arrived on time"},
		}},
	})
	wantStatus(t, rep, http.StatusOK)
	var decision struct {
		Answers []struct {
			Type        string   `json:"type"`
			Probability *float64 `json:"probability"`
		} `json:"answers"`
	}
	decodeJSON(t, rep.Body, &decision)
	if len(decision.Answers) != 1 || decision.Answers[0].Type != "predicate" {
		t.Errorf("decisions body %s, want one predicate answer", rep.Body)
	}
	wantHeader(t, rep, "X-Remora-Alias", "jev")
	wantHeader(t, rep, "X-Remora-Served-By", "ts-b/jev")
	wantHeader(t, rep, "X-Remora-Fallback", "true")

	if n := len(received(t, "ts-a", control.RouteTypeSafeSystemOne)); n != 2 {
		t.Errorf("ts-a got %d systemone requests, want 2", n)
	}
	if n := len(received(t, "ts-b", control.RouteTypeSafeSystemOne)); n != 2 {
		t.Errorf("ts-b got %d systemone requests, want 2", n)
	}
}
