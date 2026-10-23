package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Hello from Go", "path": r.URL.Path})
	})
	log.Println("listening on :8080")
	srv := &http.Server{Addr: ":8080", ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
