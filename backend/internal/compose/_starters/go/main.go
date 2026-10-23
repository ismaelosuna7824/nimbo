package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// requestDuration: HTTP server metrics named after the OpenTelemetry
// semantic conventions (http.server.request.duration in seconds), as
// Prometheus scrapes them at GET /metrics: Add monitoring's 5xx and p95
// alerts read this histogram.
var requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_server_request_duration_seconds",
	Help:    "Duration of HTTP server requests.",
	Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
}, []string{"http_request_method", "http_route", "http_response_status_code"})

func main() {
	// The image has no shell or wget: HEALTHCHECK runs the binary itself.
	healthcheck := flag.Bool("healthcheck", false, "probe the running server and exit")
	flag.Parse()
	if *healthcheck {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8080/")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Hello from Go"})
	})
	root := http.NewServeMux()
	root.Handle("GET /metrics", promhttp.Handler()) // not measured itself
	root.Handle("/", measured(mux))
	log.Println("listening on :8080")
	srv := &http.Server{Addr: ":8080", Handler: root, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// measured records every request's duration, labelled with the route
// pattern it matched ("" when none: no per-path cardinality).
func measured(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(rec, r)
		_, pattern := mux.Handler(r)
		if i := strings.IndexByte(pattern, ' '); i >= 0 {
			pattern = pattern[i+1:] // "GET /{$}" -> "/{$}"
		}
		pattern = strings.TrimSuffix(pattern, "{$}") // "/{$}" -> "/"
		requestDuration.WithLabelValues(r.Method, pattern, strconv.Itoa(rec.status)).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
