package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/kube"
	"github.com/madic-creates/homelab-toys/internal/prom"
	tamagotchiweb "github.com/madic-creates/homelab-toys/web/tamagotchi"
)

const (
	pollInterval        = 20 * time.Second
	httpReadTimeout     = 10 * time.Second
	httpWriteTimeout    = 30 * time.Second
	httpIdleTimeout     = 90 * time.Second
	shutdownGracePeriod = 10 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	argocdURL := mustEnv("ARGOCD_URL")
	argocdToken := mustEnv("ARGOCD_TOKEN")
	promURL := mustEnv("PROMETHEUS_URL")
	podName := os.Getenv("POD_NAME")
	podNS := os.Getenv("POD_NAMESPACE")
	port := envOr("PORT", "8080")

	cs, dyn, err := kube.NewInCluster()
	if err != nil {
		return fmt.Errorf("kube clients: %w", err)
	}

	// Birthday: best-effort. If POD_NAME/POD_NAMESPACE are unset (e.g.
	// running locally with kubectl proxy) or the API call fails, log and
	// proceed with a zero birthday — the page will show age 0.
	st := NewState()
	if podName != "" && podNS != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		bday, bdayErr := kube.SelfPodCreatedAt(ctx, cs, podNS, podName)
		cancel()
		if bdayErr != nil {
			slog.Warn("self-pod birthday read", "error", bdayErr)
		} else {
			st.SetBirthday(bday)
		}
	}

	// Build clients. *certs.Lister already satisfies the certsLister
	// interface (ExpiringSoon method). *kubeNodesLister wraps cs to
	// satisfy NodesLister.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	argoC := argocd.NewClient(argocdURL, argocdToken, httpClient)
	promC := prom.NewClient(promURL, httpClient)
	certsL := certs.NewLister(dyn)
	nodesL := &kubeNodesLister{cs: cs}

	// Templates: parse each file into a named template so handlers can
	// call ExecuteTemplate(w, "index", ...) and ExecuteTemplate(w, "widget", ...).
	indexBytes, err := tamagotchiweb.FS.ReadFile("index.html.tmpl")
	if err != nil {
		return fmt.Errorf("read index template: %w", err)
	}
	tpl, err := template.New("index").Parse(string(indexBytes))
	if err != nil {
		return fmt.Errorf("parse index template: %w", err)
	}
	widgetBytes, err := tamagotchiweb.FS.ReadFile("widget.html.tmpl")
	if err != nil {
		return fmt.Errorf("read widget template: %w", err)
	}
	if _, err = tpl.New("widget").Parse(string(widgetBytes)); err != nil {
		return fmt.Errorf("parse widget template: %w", err)
	}

	cssBytes, err := tamagotchiweb.FS.ReadFile("style.css")
	if err != nil {
		return fmt.Errorf("read css: %w", err)
	}

	now := time.Now
	h := NewHandlers(st, tpl, now)
	h.SetCSS(string(cssBytes))

	// Aggregator goroutines — one per source.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go runSource(ctx, "argocd", MakeArgoCDPoll(argoC, st, now), pollInterval, h)
	go runSource(ctx, "longhorn", MakeLonghornPoll(promC, st, now), pollInterval, h)
	go runSource(ctx, "certs", MakeCertsPoll(certsL, st, now), pollInterval, h)
	go runSource(ctx, "restarts", MakeRestartsPoll(promC, st, now), pollInterval, h)
	go runSource(ctx, "nodes", MakeNodesPoll(nodesL, st, now), pollInterval, h)

	// Mood ticker — independent of source goroutines, runs UpdateMood every
	// 5s so the page reflects state changes promptly without waiting for the
	// next 20s source poll.
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st.UpdateMood(now())
			}
		}
	}()

	// HTTP server.
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.Index)
	mux.HandleFunc("/widget", h.Widget)
	mux.HandleFunc("/api/state", h.APIState)
	mux.HandleFunc("/healthz", h.Healthz)
	mux.Handle("/metrics", h.Metrics())

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
		IdleTimeout:  httpIdleTimeout,
	}

	listenErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-listenErr:
		slog.Error("listener", "error", err)
	}

	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		slog.Error("required env var missing", "key", k)
		os.Exit(2)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// kubeNodesLister adapts a kubernetes.Interface clientset into the
// NodesLister surface that MakeNodesPoll expects. The internal/kube
// package's Nodes(ctx, cs) function does the actual list + Ready
// extraction; this wrapper just pins the clientset.
type kubeNodesLister struct{ cs kubernetes.Interface }

func (n *kubeNodesLister) Nodes(ctx context.Context) ([]kube.NodeStatus, error) {
	return kube.Nodes(ctx, n.cs)
}
