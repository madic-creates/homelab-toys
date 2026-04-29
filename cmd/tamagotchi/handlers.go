package main

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handlers wires HTTP endpoints to the shared State + templates.
type Handlers struct {
	state        *State
	tpl          *template.Template
	now          func() time.Time
	processStart time.Time

	cssPayload template.CSS

	pollTotal       *prometheus.CounterVec
	lastSuccessSecs *prometheus.GaugeVec
	moodLevel       prometheus.Gauge
	renderDuration  prometheus.Histogram
}

// NewHandlers constructs the handlers. tpl is allowed to be nil so unit
// tests can target APIState/healthz without parsing the real templates.
func NewHandlers(s *State, tpl *template.Template, now func() time.Time) *Handlers {
	return &Handlers{
		state:        s,
		tpl:          tpl,
		now:          now,
		processStart: now(),
		pollTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "tamagotchi_source_poll_total"},
			[]string{"source", "result"},
		),
		lastSuccessSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "tamagotchi_source_last_success_seconds"},
			[]string{"source"},
		),
		moodLevel: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "tamagotchi_mood_level"},
		),
		renderDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{Name: "tamagotchi_render_duration_seconds"},
		),
	}
}

// SetCSS installs the per-page CSS string. Wired up from main.go.
func (h *Handlers) SetCSS(css string) { h.cssPayload = template.CSS(css) }

// PollTotal increments per-source poll counters. Implements metricsRecorder.
func (h *Handlers) PollTotal(source, result string) {
	h.pollTotal.WithLabelValues(source, result).Inc()
}

// LastSuccessSeconds records the unix-second timestamp of the last
// successful poll. Implements metricsRecorder.
func (h *Handlers) LastSuccessSeconds(source string, seconds float64) {
	h.lastSuccessSecs.WithLabelValues(source).Set(seconds)
}

// APIState serves a JSON snapshot — see the spec's response shape.
func (h *Handlers) APIState(w http.ResponseWriter, _ *http.Request) {
	snap := h.state.Snapshot()
	// factors is deferred to v2; v1 communicates source contributions
	// only via stale_sources. Returning an empty array (rather than
	// omitting the key) keeps the contract stable — consumers can
	// observe an empty slice and know their parser is correct.
	resp := struct {
		Mood         string   `json:"mood"`
		MoodLevel    int      `json:"mood_level"`
		AgeDays      int      `json:"age_days"`
		BornAt       string   `json:"born_at"`
		Factors      []any    `json:"factors"`
		StaleSources []string `json:"stale_sources"`
		Confused     bool     `json:"confused"`
		Hello        bool     `json:"hello"`
	}{
		Mood:         snap.Mood.Name(),
		MoodLevel:    snap.Mood.Level,
		AgeDays:      ageInDays(snap.Birthday, h.now()),
		BornAt:       formatBirthday(snap.Birthday),
		Factors:      []any{},
		StaleSources: snap.StaleSources,
		Confused:     snap.Confused,
		Hello:        !snap.HasFirstTick,
	}
	if resp.StaleSources == nil {
		resp.StaleSources = []string{} // ensure JSON `[]`, not `null`
	}

	// Update the mood-level gauge while we're holding a fresh snapshot.
	h.moodLevel.Set(float64(snap.Mood.Level))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("encode /api/state", "error", err)
	}
}

type pageData struct {
	Mood     string
	Level    int
	AgeDays  int
	Sprite   template.HTML
	Hello    bool
	Confused bool
	Stale    []string
	CSS      template.CSS
}

// Index serves the fullscreen page. The template name is "index".
func (h *Handlers) Index(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "index")
}

// Widget serves the compact widget page. The template name is "widget".
func (h *Handlers) Widget(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "widget")
}

func (h *Handlers) renderPage(w http.ResponseWriter, name string) {
	now := h.now()
	defer func() {
		h.renderDuration.Observe(time.Since(now).Seconds())
	}()
	snap := h.state.Snapshot()
	data := pageData{
		Mood:     snap.Mood.Name(),
		Level:    snap.Mood.Level,
		AgeDays:  ageInDays(snap.Birthday, now),
		Sprite:   template.HTML(RenderSprite(snap.Mood.Name(), snap.Confused)), //nolint:gosec // RenderSprite produces controlled SVG markup
		Hello:    !snap.HasFirstTick,
		Confused: snap.Confused,
		Stale:    snap.StaleSources,
		CSS:      h.cssPayload,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Warn("render template", "name", name, "error", err)
	}
}

func ageInDays(birthday, now time.Time) int {
	if birthday.IsZero() {
		return 0
	}
	return int(now.Sub(birthday) / (24 * time.Hour))
}

func formatBirthday(birthday time.Time) string {
	if birthday.IsZero() {
		return ""
	}
	return birthday.UTC().Format(time.RFC3339)
}

const healthzWindow = 90 * time.Second

// Healthz returns 200 if every source's last successful poll is within
// healthzWindow, else 503. During init grace (no source loaded yet)
// returns 200 to avoid blocking readiness before the first poll.
func (h *Handlers) Healthz(w http.ResponseWriter, _ *http.Request) {
	snap := h.state.Snapshot()
	now := h.now()

	slots := []Slot[int]{snap.ArgoCD, snap.Longhorn, snap.Certs, snap.Restarts, snap.Nodes}
	anyLoaded := false
	for _, s := range slots {
		if s.Loaded {
			anyLoaded = true
			break
		}
	}
	if !anyLoaded {
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, s := range slots {
		if !s.Loaded || now.Sub(s.LastSuccess) > healthzWindow {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// Metrics returns a Prometheus-handler that serves the four tamagotchi
// collectors. Each call allocates a fresh registry, so multiple calls
// are safe but wasteful — typically called once in main() and mounted
// on /metrics.
func (h *Handlers) Metrics() http.Handler {
	r := prometheus.NewRegistry()
	r.MustRegister(h.pollTotal, h.lastSuccessSecs, h.moodLevel, h.renderDuration)
	return promhttp.HandlerFor(r, promhttp.HandlerOpts{})
}
