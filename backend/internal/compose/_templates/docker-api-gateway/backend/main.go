// A tiny HTTP service that answers with its own name and container
// hostname, so you can see which service the gateway routed to. With
// AGGREGATE set ("users=http://users:8080/,orders=http://orders:8080/") it
// calls those services and returns their answers in one response.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
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

	name := os.Getenv("SERVICE_NAME")
	host, _ := os.Hostname()
	parts := parseParts(os.Getenv("AGGREGATE"))
	client := &http.Client{Timeout: 3 * time.Second}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"service":  name,
			"hostname": host,
			"path":     r.URL.Path,
			// Set by the gateway once the API key is checked.
			"consumer": r.Header.Get("X-Consumer-Username"),
		}
		status := http.StatusOK
		if len(parts) > 0 {
			got, ok := fetchAll(r.Context(), client, parts)
			body["parts"] = got
			if !ok {
				status = http.StatusBadGateway
			}
		}
		writeJSON(w, status, body)
	})
	log.Printf("%s listening on :8080", name)
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// parseParts reads "name=url,name=url".
func parseParts(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if name, url, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
			out[name] = url
		}
	}
	return out
}

// fetchAll calls every part at once; a part that fails holds its error.
func fetchAll(ctx context.Context, client *http.Client, parts map[string]string) (map[string]any, bool) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := map[string]any{}
	ok := true
	for name, url := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var v any
			err := getJSON(ctx, client, url, &v)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[name] = map[string]string{"error": err.Error()}
				ok = false
				return
			}
			out[name] = v
		}()
	}
	wg.Wait()
	return out, ok
}

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
