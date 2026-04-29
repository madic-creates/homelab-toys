package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func TestAPIState_HelloWhileNotYetTicked(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.UpdateMood(now) // no sources Loaded → init grace
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.APIState(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	var got apiStateResponse
	json.Unmarshal(rec.Body.Bytes(), &got) //nolint:errcheck
	if !got.Hello {
		t.Errorf("Hello = false, want true during init grace")
	}
	if got.Mood != "happy" {
		t.Errorf("init mood = %q, want happy", got.Mood)
	}
}
