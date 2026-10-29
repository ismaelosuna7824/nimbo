// Composition root + HTTP server driving adapter (containers).
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"example.com/api/internal/adapters/inbound/httpapi"
	"example.com/api/internal/adapters/outbound/store"
	"example.com/api/internal/application"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	repo, err := store.New(context.Background(), os.Getenv)
	if err != nil {
		log.Error("startup", "error", err)
		os.Exit(1)
	}
	items := application.Items{Repo: repo, NewID: rand.Text, Now: time.Now}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id") // correlation id
		if id == "" {
			id = rand.Text()
		}
		reqLog := log.With("requestId", id)
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		res := httpapi.Routes{Items: items, Log: reqLog}.Handle(r.Context(), r.Method, r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", id)
		w.WriteHeader(res.Status)
		_ = json.NewEncoder(w).Encode(res.Body)
		reqLog.Info("request", "path", r.URL.Path, "status", res.Status)
	})
	addr := ":" + envOr("PORT", "8080")
	log.Info("listening", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server", "error", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
