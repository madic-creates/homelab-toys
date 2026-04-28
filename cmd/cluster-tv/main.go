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

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/kube"
	"github.com/madic-creates/homelab-toys/internal/prom"
	webclustertv "github.com/madic-creates/homelab-toys/web/cluster-tv"
	"github.com/prometheus/client_golang/prometheus"
)

const tickInterval = 20 * time.Second
const certWindow = 30 * 24 * time.Hour

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(context.Background()); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(parent context.Context) error {
	port := envOrDefault("PORT", "8080")
	argoURL := mustEnv("ARGOCD_URL")
	argoToken := mustEnv("ARGOCD_TOKEN")
	promURL := mustEnv("PROMETHEUS_URL")

	cs, dyn, err := kube.NewInCluster()
	if err != nil {
		return fmt.Errorf("kube clients: %w", err)
	}
	_ = cs // typed clientset reserved for future use; cert-manager goes via dynamic

	state := NewState()
	httpClient := &http.Client{Timeout: 10 * time.Second}

	argoCli := argocd.NewClient(argoURL, argoToken, httpClient)
	promCli := prom.NewClient(promURL, httpClient)
	certLister := certs.NewLister(dyn)

	tpl, err := template.ParseFS(webclustertv.FS, "index.html.tmpl")
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}
	crtCSS, err := webclustertv.FS.ReadFile("crt.css")
	if err != nil {
		return fmt.Errorf("read crt.css: %w", err)
	}
	modernCSS, err := webclustertv.FS.ReadFile("modern.css")
	if err != nil {
		return fmt.Errorf("read modern.css: %w", err)
	}

	now := time.Now
	handlers := NewHandlers(state, tpl, now)
	handlers.SetCSS(string(crtCSS), string(modernCSS))

	reg := prometheus.NewRegistry()
	handlers.Register(reg)

	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go runSource(ctx, "argocd",   MakeArgoCDPoll(argoCli, state, now), tickInterval)
	go runSource(ctx, "longhorn", MakeLonghornPoll(promCli, state, now), tickInterval)
	go runSource(ctx, "restarts", MakeRestartsPoll(promCli, state, now), tickInterval)
	go runSource(ctx, "certs",    MakeCertsPoll(certLister, certWindow, state, now), tickInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handlers.HandleIndex)
	mux.HandleFunc("/api/state", handlers.HandleAPIState)
	mux.HandleFunc("/healthz", handlers.HandleHealthz)
	mux.Handle("/metrics", handlers.MetricsHandler(reg))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer c2()
		return srv.Shutdown(shutdownCtx)
	}
}

func envOrDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		slog.Error("required env var missing", "key", k)
		os.Exit(2)
	}
	return v
}
