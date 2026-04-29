package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type apiStateResponse struct {
	Mood         string   `json:"mood"`
	MoodLevel    int      `json:"mood_level"`
	AgeDays      int      `json:"age_days"`
	BornAt       string   `json:"born_at"`
	StaleSources []string `json:"stale_sources"`
	Confused     bool     `json:"confused"`
	Hello        bool     `json:"hello"`
}

func TestAPIState_HappyPath(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetBirthday(now.Add(-3 * 24 * time.Hour))
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(0, now)
	st.UpdateMood(now)

	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	h.APIState(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got apiStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Mood != "ecstatic" || got.MoodLevel != 0 {
		t.Errorf("mood = %q level=%d, want ecstatic 0", got.Mood, got.MoodLevel)
	}
	if got.AgeDays != 3 {
		t.Errorf("age = %d, want 3", got.AgeDays)
	}
	if got.Hello {
		t.Errorf("Hello should be false after first tick")
	}
	// Lock in the [] vs null normalization — apiStateResponse would
	// happily decode either, so we have to check the raw body.
	if !strings.Contains(rec.Body.String(), `"stale_sources":[]`) {
		t.Errorf("stale_sources should serialise as []; body = %s", rec.Body.String())
	}
}

func TestAPIState_HelloWhileNotYetTicked(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.UpdateMood(now) // no sources Loaded → init grace
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.APIState(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	var got apiStateResponse
	json.Unmarshal(rec.Body.Bytes(), &got) //nolint:errcheck // body is handler-produced JSON; parse failure would surface as zero-value mood/hello below
	if !got.Hello {
		t.Errorf("Hello = false, want true during init grace")
	}
	if got.Mood != "happy" {
		t.Errorf("init mood = %q, want happy", got.Mood)
	}
}

func TestIndex_RendersSpriteAndMoodClass(t *testing.T) {
	tpl := template.Must(template.New("index").Parse(`<body class="mood-{{.Mood}}">{{.Sprite}}</body>`))
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(0, now)
	st.UpdateMood(now)

	h := NewHandlers(st, tpl, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Index(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `class="mood-ecstatic"`) {
		t.Errorf("body missing mood class: %s", body)
	}
	if !strings.Contains(body, `<svg class="sprite mood-ecstatic"`) {
		t.Errorf("body missing sprite: %s", body)
	}
}

func TestWidget_RendersSpriteAndMoodText(t *testing.T) {
	tpl := template.Must(template.New("widget").Parse(`<body>{{.Sprite}}<span>{{.Mood}}</span></body>`))
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(3, now) // node down → dying
	st.UpdateMood(now)

	h := NewHandlers(st, tpl, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Widget(rec, httptest.NewRequest(http.MethodGet, "/widget", nil))

	body := rec.Body.String()
	// Note: penalty 3 + clamp gives Mood.Level=3 (sick), not 4 (dying).
	// Use "sick" in assertions.
	if !strings.Contains(body, `<svg class="sprite mood-sick"`) {
		t.Errorf("widget missing sprite: %s", body)
	}
	if !strings.Contains(body, ">sick</span>") {
		t.Errorf("widget missing mood text: %s", body)
	}
}

func TestHealthz_AllFreshOK(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	for _, fn := range []func(int, time.Time){
		st.SetArgoCD, st.SetLonghorn, st.SetCerts, st.SetRestarts, st.SetNodes,
	} {
		fn(0, now)
	}
	h := NewHandlers(st, nil, func() time.Time { return now.Add(30 * time.Second) })
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHealthz_StaleSourceFails(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(0, now.Add(-2*time.Minute)) // > 90s old
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHealthz_InitGraceOK(t *testing.T) {
	// No source loaded yet — /healthz should still be 200 so readiness
	// doesn't fail before the first poll arrives.
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("init grace status = %d, want 200", rec.Code)
	}
}
