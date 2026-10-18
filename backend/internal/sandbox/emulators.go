package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// emulator describes how one Floci emulator is run and probed.
type emulator struct {
	cloud     model.Cloud
	image     string
	port      int
	envPrefix string // Quarkus config prefix as env var: FLOCI, FLOCI_AZ, ...
	readyPath string
	// ready reports readiness from a 200 response body of readyPath.
	ready      func(body []byte) bool
	healthPath string // {version, services}
	resetPath  string // POST
	extraEnv   []string
}

var emulators = map[model.Cloud]emulator{
	model.CloudAWS: {
		cloud: model.CloudAWS, image: "floci/floci:latest", port: 4566, envPrefix: "FLOCI",
		readyPath: "/_floci/init",
		ready: func(body []byte) bool {
			var v struct {
				Completed struct {
					Ready bool `json:"ready"`
				} `json:"completed"`
			}
			return json.Unmarshal(body, &v) == nil && v.Completed.Ready
		},
		healthPath: "/_floci/health",
		resetPath:  "/_floci/state/reset",
	},
	model.CloudAzure: {
		cloud: model.CloudAzure, image: "floci/floci-az:latest", port: 4577, envPrefix: "FLOCI_AZ",
		readyPath:  "/ready",
		healthPath: "/health",
		resetPath:  "/_admin/reset",
		extraEnv:   []string{"FLOCI_AZ_TLS_ENABLED=true"},
	},
	model.CloudGCP: {
		cloud: model.CloudGCP, image: "floci/floci-gcp:latest", port: 4588, envPrefix: "FLOCI_GCP",
		readyPath:  "/_floci-gcp/health",
		healthPath: "/_floci-gcp/health",
		resetPath:  "/_floci-gcp/state/reset",
	},
	model.CloudOCI: {
		cloud: model.CloudOCI, image: "floci/floci-oci:latest", port: 4599, envPrefix: "FLOCI_OCI",
		readyPath:  "/_floci-oci/health",
		healthPath: "/_floci-oci/health",
		resetPath:  "/_floci-oci/state/reset",
	},
}

// emulatorEnv is the container environment of an emulator in workflow wfID.
// Persistent storage keeps emulator state across stop/start in step with the
// Terraform state; the resource namespace keeps each sandbox's spawned
// containers apart from other Floci instances sharing the Docker daemon.
func emulatorEnv(e emulator, wfID string) []string {
	p := e.envPrefix
	env := []string{
		p + "_STORAGE_MODE=persistent",
		p + "_STORAGE_PERSISTENT_PATH=/app/data",
		p + "_SERVICES_DOCKER_NETWORK=" + networkName(wfID),
		p + "_DOCKER_RESOURCE_NAMESPACE=" + networkName(wfID),
	}
	if e.cloud == model.CloudAWS {
		env = append(env,
			"FLOCI_DOCKER_EXTRA_LABELS_0__KEY="+labelWorkflow,
			"FLOCI_DOCKER_EXTRA_LABELS_0__VALUE="+wfID,
		)
	}
	return append(env, e.extraEnv...)
}

var probeClient = &http.Client{Timeout: 3 * time.Second}

func httpGet(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, err
}

func httpPost(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// probeReady reports whether the emulator at base answers its readiness check.
func probeReady(ctx context.Context, e emulator, base string) bool {
	code, body, err := httpGet(ctx, base+e.readyPath)
	if err != nil || code != http.StatusOK {
		return false
	}
	return e.ready == nil || e.ready(body)
}

// probeHealth reads version and service map from the health endpoint.
func probeHealth(ctx context.Context, e emulator, base string) (string, map[string]string, bool) {
	code, body, err := httpGet(ctx, base+e.healthPath)
	if err != nil || code != http.StatusOK {
		return "", nil, false
	}
	var h struct {
		Version  string            `json:"version"`
		Services map[string]string `json:"services"`
	}
	_ = json.Unmarshal(body, &h)
	return h.Version, h.Services, true
}
