package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// HostURL rewrites a URL as seen inside the sandbox (or as reported by the
// emulator, e.g. API Gateway and Lambda URL hosts) to one reachable from the
// host. Unknown URLs are returned unchanged.
func (m *Manager) HostURL(wfID, emulatorURL string) string {
	return rewriteURL(emulatorURL, managerResolver{m, wfID})
}

// urlResolver supplies sandbox facts needed to rewrite URLs.
type urlResolver interface {
	// hostPort maps a native emulator port to its published host port (0
	// when unknown).
	hostPort(port int) int
	// apiStages lists the stage names of an API Gateway API (nil when
	// unknown).
	apiStages(apiID string) []string
}

type managerResolver struct {
	m    *Manager
	wfID string
}

func (r managerResolver) hostPort(port int) int { return r.m.hostPort(r.wfID, port) }
func (r managerResolver) apiStages(apiID string) []string {
	return r.m.apiStages(r.wfID, apiID)
}

var nativePorts = map[int]bool{4566: true, 4577: true, 4588: true, 4599: true}

func rewriteURL(raw string, r urlResolver) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	port, _ := strconv.Atoi(u.Port())

	if id, kind, ok := awsEndpointHost(host); ok {
		if port != 0 && port != 4566 {
			return raw
		}
		hp := r.hostPort(4566)
		if hp == 0 {
			return raw
		}
		rest := strings.TrimPrefix(u.EscapedPath(), "/")
		var p string
		switch kind {
		case "execute-api":
			p = "/execute-api/" + id + "/" + stagePath(rest, r.apiStages(id))
		case "lambda-url":
			p = "/lambda-url/" + id + "/" + rest
		}
		out := url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(hp), RawQuery: u.RawQuery, Fragment: u.Fragment}
		if pu, err := url.Parse(p); err == nil {
			out.Path, out.RawPath = pu.Path, pu.RawPath
		}
		return out.String()
	}

	if !nativePorts[port] {
		return raw
	}
	var newHost string
	switch {
	case host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0":
		newHost = "127.0.0.1"
	case strings.HasSuffix(host, ".localhost.floci.io") || strings.HasSuffix(host, ".localhost"):
		newHost = host
	default:
		return raw
	}
	hp := r.hostPort(port)
	if hp == 0 {
		return raw
	}
	out := *u
	out.Scheme = "http"
	out.Host = newHost + ":" + strconv.Itoa(hp)
	return out.String()
}

// stagePath turns the path of an API Gateway host-form URL into
// "<stage>/<path>" with AWS semantics: a first segment naming a stage
// selects it, otherwise the $default stage serves the whole path. Without
// a stage list the first segment is taken as the stage.
func stagePath(rest string, stages []string) string {
	first, _, _ := strings.Cut(rest, "/")
	switch {
	case first != "" && slices.Contains(stages, first):
		return rest
	case slices.Contains(stages, "$default") || rest == "":
		return "$default/" + rest
	default:
		return rest
	}
}

// awsEndpointHost recognizes API Gateway and Lambda Function URL hosts:
// <id>.execute-api.{localhost.floci.io|localhost|<region>.amazonaws.com} and
// <id>.lambda-url.{localhost.floci.io|localhost|<region>.on.aws}.
func awsEndpointHost(host string) (id, kind string, ok bool) {
	for _, k := range []string{"execute-api", "lambda-url"} {
		before, after, found := strings.Cut(host, "."+k+".")
		if !found || before == "" || strings.Contains(before, ".") {
			continue
		}
		switch {
		case after == "localhost" || after == "localhost.floci.io":
		case k == "execute-api" && strings.HasSuffix(after, ".amazonaws.com"):
		case k == "lambda-url" && strings.HasSuffix(after, ".on.aws"):
		default:
			continue
		}
		return before, k, true
	}
	return "", "", false
}

type stageEntry struct {
	stages []string
	at     time.Time
}

const stageCacheTTL = 15 * time.Second

var validAPIID = regexp.MustCompile(`^[a-zA-Z0-9]{1,64}$`)

// apiStages lists stages of an HTTP/WebSocket (v2) or REST (v1) API from the
// AWS emulator, cached briefly because HostURL may be called per request.
func (m *Manager) apiStages(wfID, apiID string) []string {
	if !validAPIID.MatchString(apiID) {
		return nil
	}
	key := wfID + "/" + apiID
	m.mu.Lock()
	if e, ok := m.stageCache[key]; ok && time.Since(e.at) < stageCacheTTL {
		m.mu.Unlock()
		return e.stages
	}
	m.mu.Unlock()
	base, ok := m.EmulatorURL(wfID, model.CloudAWS)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stages, ok := fetchStages(ctx, base+"/v2/apis/"+apiID+"/stages")
	if !ok {
		stages, ok = fetchStages(ctx, base+"/restapis/"+apiID+"/stages")
	}
	if !ok {
		return nil
	}
	m.mu.Lock()
	m.stageCache[key] = stageEntry{stages: stages, at: time.Now()}
	m.mu.Unlock()
	return stages
}

type stageList []struct {
	StageName string `json:"stageName"`
}

// fetchStages reads a stage list; v2 responses use "items", v1 "item".
func fetchStages(ctx context.Context, url string) ([]string, bool) {
	code, body, err := httpGet(ctx, url)
	if err != nil || code != http.StatusOK {
		return nil, false
	}
	var doc struct {
		Items stageList `json:"items"`
		Item  stageList `json:"item"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, false
	}
	stages := []string{}
	for _, s := range append(doc.Items, doc.Item...) {
		stages = append(stages, s.StageName)
	}
	return stages, true
}
