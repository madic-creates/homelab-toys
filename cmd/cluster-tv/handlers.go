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

// healthzWindow is the heartbeat freshness threshold for /healthz. The
// stalenessWindow constant in state.go (5 min) is for "is this tile
// trustworthy"; healthz is a tighter "is the binary alive" signal.
const healthzWindow = 90 * time.Second

// initGrace is how long after process start /healthz tolerates sources
// that have not yet loaded. Matches the 30s init phase in the spec.
const initGrace = 30 * time.Second

type Handlers struct {
	state        *State
	tpl          *template.Template
	now          func() time.Time
	processStart time.Time

	// CSS payloads injected into the template per request based on the
	// requested theme. template.CSS marks the string as already-safe so the
	// html/template package doesn't escape it.
	cssCRT    template.CSS
	cssModern template.CSS

	// metrics
	pollTotal       *prometheus.CounterVec
	lastSuccessSecs *prometheus.GaugeVec
	renderDuration  prometheus.Histogram
}

func NewHandlers(s *State, tpl *template.Template, now func() time.Time) *Handlers {
	return &Handlers{
		state:        s,
		tpl:          tpl,
		now:          now,
		processStart: now(),
		pollTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "cluster_tv_source_poll_total"},
			[]string{"source", "result"},
		),
		lastSuccessSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "cluster_tv_source_last_success_seconds"},
			[]string{"source"},
		),
		renderDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{Name: "cluster_tv_render_duration_seconds"},
		),
	}
}

// SetCSS installs the per-theme CSS strings injected into the index
// template. Wired up from cmd/cluster-tv/main.go using the assets in the
// web/cluster-tv embed.FS — kept off the constructor so handler tests can
// run with a stand-in template that doesn't reference {{.CSS}}.
func (h *Handlers) SetCSS(crt, modern string) {
	h.cssCRT = template.CSS(crt)
	h.cssModern = template.CSS(modern)
}

// Register registers metric collectors with the given registry. Tests can
// pass a fresh registry to avoid global-state collisions.
func (h *Handlers) Register(reg prometheus.Registerer) {
	reg.MustRegister(h.pollTotal, h.lastSuccessSecs, h.renderDuration)
}

// MetricsHandler returns the http.Handler for /metrics. Use the same
// registry that was passed to Register.
func (h *Handlers) MetricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// PollTotal lets aggregator goroutines record their success/failure counts.
func (h *Handlers) PollTotal(source, result string) {
	h.pollTotal.WithLabelValues(source, result).Inc()
}

// LastSuccessSeconds publishes the per-source heartbeat age.
func (h *Handlers) LastSuccessSeconds(source string, seconds float64) {
	h.lastSuccessSecs.WithLabelValues(source).Set(seconds)
}

// ---------- /api/state ----------

func (h *Handlers) HandleAPIState(w http.ResponseWriter, _ *http.Request) {
	snap := h.state.Snapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(snap); err != nil {
		slog.Warn("api/state encode failed", "error", err)
	}
}

// ---------- /healthz ----------

// HandleHealthz returns 200 iff every loaded source has a heartbeat newer
// than healthzWindow OR the process is still in its init grace and the
// source has never loaded. 503 otherwise.
func (h *Handlers) HandleHealthz(w http.ResponseWriter, _ *http.Request) {
	now := h.now()
	snap := h.state.Snapshot()
	inGrace := now.Sub(h.processStart) < initGrace

	check := func(loaded bool, last time.Time) bool {
		if !loaded {
			return inGrace
		}
		return now.Sub(last) <= healthzWindow
	}

	ok := check(snap.ArgoCD.Loaded, snap.ArgoCD.LastSuccess) &&
		check(snap.Longhorn.Loaded, snap.Longhorn.LastSuccess) &&
		check(snap.Certs.Loaded, snap.Certs.LastSuccess) &&
		check(snap.Restarts.Loaded, snap.Restarts.LastSuccess)

	if !ok {
		http.Error(w, "stale source", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// ---------- / ----------

type indexData struct {
	Theme    string
	CSS      template.CSS
	S        Snapshot
	AllGreen bool
	Stale    int
	Now      time.Time
}

func (h *Handlers) HandleIndex(w http.ResponseWriter, r *http.Request) {
	now := h.now()
	defer func() { h.renderDuration.Observe(time.Since(now).Seconds()) }()

	theme := r.URL.Query().Get("theme")
	if theme != "modern" {
		theme = "crt" // default + fallback for invalid values
	}

	css := h.cssCRT
	if theme == "modern" {
		css = h.cssModern
	}
	data := indexData{
		Theme:    theme,
		CSS:      css,
		S:        h.state.Snapshot(),
		AllGreen: h.state.AllGreen(now),
		Stale:    h.state.StaleCount(now),
		Now:      now,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
