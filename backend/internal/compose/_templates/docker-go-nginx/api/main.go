package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the running server and exit")
	flag.Parse()
	if *healthcheck {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8080/health")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	greeting := os.Getenv("GREETING")
	if greeting == "" {
		greeting = "Hello"
	}
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"message": greeting,
			"path":    r.URL.Path,
			"hits":    hits.Add(1),
			"via":     r.Header.Get("X-Real-IP"),
		})
	})
	log.Println("api listening on :8080")
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
