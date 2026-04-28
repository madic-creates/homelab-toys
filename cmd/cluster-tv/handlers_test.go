package main

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// minimal stand-in templates so handler tests don't depend on the real
// embedded files (those are loaded via embed.FS in main.go).
func testTemplates(t *testing.T) *template.Template {
	t.Helper()
	tpl, err := template.New("index").Parse(`<!doctype html><html data-theme="{{.Theme}}"><body><h1>cluster-tv</h1>
<div id="argocd">{{.S.ArgoCD.Data.Healthy}} healthy</div>
{{if .AllGreen}}<div id="ok">CLUSTER OK</div>{{end}}
</body></html>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tpl
}

func TestHandleAPIState_AlwaysReturnsJSON(t *testing.T) {
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 5}, time.Now())
	h := NewHandlers(s, testTemplates(t), time.Now)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	h.HandleAPIState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("content-type = %q", got)
	}
	var snap Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.ArgoCD.Data.Healthy != 5 {
		t.Errorf("ArgoCD.Healthy = %d, want 5", snap.ArgoCD.Data.Healthy)
	}
}

func TestHandleHealthz_AlwaysReturns200(t *testing.T) {
	// /healthz is a liveness probe — it must NOT depend on data freshness.
	// Verify across every interesting State configuration.
	now := time.Now()
	cases := []struct {
		name string
		set  func(*State)
	}{
		{"empty state", func(s *State) {}},
		{"all fresh", func(s *State) {
			s.SetArgoCD(ArgoCDData{}, now)
			s.SetLonghorn(LonghornData{}, now)
			s.SetCerts(CertsData{}, now)
			s.SetRestarts(RestartsData{}, now)
		}},
		{"all stale", func(s *State) {
			old := now.Add(-1 * time.Hour)
			s.SetArgoCD(ArgoCDData{}, old)
			s.SetLonghorn(LonghornData{}, old)
			s.SetCerts(CertsData{}, old)
			s.SetRestarts(RestartsData{}, old)
		}},
		{"one source has only ever errored (Loaded=false, LastError set)", func(s *State) {
			s.SetArgoCDError(errors.New("argocd timeout"), now)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewState()
			tc.set(s)
			h := NewHandlers(s, testTemplates(t), func() time.Time { return now })
			rec := httptest.NewRecorder()
			h.HandleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			if rec.Body.String() != "ok" {
				t.Errorf("body = %q, want \"ok\"", rec.Body.String())
			}
		})
	}
}

func TestHandleIndex_ThemeQuery(t *testing.T) {
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 7}, time.Now())
	h := NewHandlers(s, testTemplates(t), time.Now)

	for _, theme := range []string{"crt", "modern"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/?theme="+theme, nil)
		h.HandleIndex(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("theme=%s: status = %d", theme, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `data-theme="`+theme+`"`) {
			t.Errorf("theme=%s: body missing data-theme attribute. body=%q", theme, rec.Body.String())
		}
	}
}

func TestHandleIndex_DefaultThemeIsCRT(t *testing.T) {
	s := NewState()
	h := NewHandlers(s, testTemplates(t), time.Now)
	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `data-theme="crt"`) {
		t.Errorf("default theme not crt. body=%q", rec.Body.String())
	}
}

func TestHandleIndex_InvalidThemeFallsBackToCRT(t *testing.T) {
	s := NewState()
	h := NewHandlers(s, testTemplates(t), time.Now)
	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/?theme=neon", nil))
	if !strings.Contains(rec.Body.String(), `data-theme="crt"`) {
		t.Errorf("invalid theme should fall back to crt. body=%q", rec.Body.String())
	}
}

func TestHandleIndex_AllGreenShowsBanner(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 1}, now)
	s.SetLonghorn(LonghornData{Healthy: 1}, now)
	s.SetCerts(CertsData{Total: 0}, now)
	s.SetRestarts(RestartsData{Total: 0}, now)

	h := NewHandlers(s, testTemplates(t), func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "CLUSTER OK") {
		t.Errorf("AllGreen banner missing. body=%q", rec.Body.String())
	}
}
