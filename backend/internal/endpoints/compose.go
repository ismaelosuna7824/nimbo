package endpoints

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// Docker workflows: one ResourceAccess per compose service with published
// ports, built from the project's live containers (sandbox.ComposeInspect).
// Address and node ID are the graph node ID ("service:<name>").

// dbKind classifies a service as a database or cache by its image, else by
// its container ports.
func dbKind(image string, ports []model.ComposePort) string {
	ref := strings.ToLower(image)
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i] // tag; a registry port comes before a "/"
	}
	for _, c := range []struct{ match, kind string }{
		{"postgis", "postgres"}, {"timescale", "postgres"}, {"postgres", "postgres"},
		{"mariadb", "mysql"}, {"mysql", "mysql"}, {"percona", "mysql"},
		{"mongo", "mongo"}, {"mssql", "sqlserver"}, {"sqlserver", "sqlserver"}, {"azure-sql-edge", "sqlserver"},
		{"valkey", "redis"}, {"keydb", "redis"}, {"redis", "redis"}, {"memcached", "memcached"},
	} {
		if strings.Contains(ref, c.match) {
			return c.kind
		}
	}
	for _, p := range ports {
		switch p.Container {
		case 5432:
			return "postgres"
		case 3306:
			return "mysql"
		case 27017:
			return "mongo"
		case 1433:
			return "sqlserver"
		case 6379:
			return "redis"
		case 11211:
			return "memcached"
		}
	}
	return ""
}

var dbPort = map[string]int{"postgres": 5432, "mysql": 3306, "mongo": 27017, "sqlserver": 1433, "redis": 6379, "memcached": 11211}

// composeEntry is a service card plus the HTTP URLs Try may call.
type composeEntry struct {
	model.ResourceAccess
	urls []string
}

// BuildCompose describes how to reach each running compose service from
// the host. rt comes from sandbox.Manager.ComposeInspect.
func BuildCompose(wf *model.Workflow, rt sandbox.ComposeRuntime) model.EndpointsInfo {
	info := model.EndpointsInfo{WorkflowID: rt.Status.WorkflowID, Sandbox: []model.SandboxAccess{}, Resources: []model.ResourceAccess{}, UpdatedAt: time.Now().UTC()}
	if wf != nil && info.WorkflowID == "" {
		info.WorkflowID = wf.ID
	}
	for _, e := range composeEntries(rt) {
		info.Resources = append(info.Resources, e.ResourceAccess)
	}
	return info
}

func composeEntries(rt sandbox.ComposeRuntime) []composeEntry {
	var out []composeEntry
	for _, svc := range rt.Status.Services {
		var ports []model.ComposePort
		var containers []string
		image := svc.Image
		for _, c := range svc.Containers {
			containers = append(containers, c.Name)
			ports = append(ports, c.Ports...)
			if image == "" {
				image = c.Image
			}
		}
		if len(ports) == 0 {
			continue
		}
		out = append(out, composeCard(svc, image, containers, ports, rt.Env[svc.Name]))
	}
	return out
}

func composeCard(svc model.ComposeServiceStatus, image string, containers []string, ports []model.ComposePort, env map[string]string) composeEntry {
	e := composeEntry{ResourceAccess: model.ResourceAccess{
		NodeID: svc.NodeID, Address: svc.NodeID, Type: image, Label: svc.Name,
		Fields: []model.AccessField{}, Snippets: []model.Snippet{}, Actions: []model.TryAction{},
	}}
	field := func(label, value, hint string, secret bool) {
		if value != "" {
			e.Fields = append(e.Fields, model.AccessField{Label: label, Value: value, Hint: hint, Secret: secret})
		}
	}
	snippet := func(label, lang, code string) {
		e.Snippets = append(e.Snippets, model.Snippet{Label: label, Language: lang, Code: code})
	}
	container := containers[0]
	field("Container", strings.Join(containers, ", "), "Docker container name: docker logs / exec use it.", false)
	field("State", composeStateText(svc), "", false)
	if svc.State != model.ComposeRunning {
		e.Warning = "This service is not running (" + string(svc.State) + "): run Up to start it; its host ports answer only while it runs."
	}

	// The compose image only: built images are named after the project,
	// whose name may contain anything; they fall back to the ports.
	if kind := dbKind(svc.Image, ports); kind != "" {
		hostPort := 0
		for _, p := range ports {
			if p.Protocol == "tcp" && p.URL != "" && (hostPort == 0 || p.Container == dbPort[kind]) {
				hostPort = p.Host
			}
		}
		e.Kind = KindDatabase
		if kind == "redis" || kind == "memcached" {
			e.Kind = KindCache
		}
		dbFields(&e, kind, hostPort, container, env, field, snippet)
		otherPorts(ports, dbPort[kind], field)
		return e
	}

	var curls []string
	for _, p := range ports {
		if sandbox.ComposeHTTPPort(p) {
			e.urls = append(e.urls, p.URL)
			field(fmt.Sprintf("Host URL (port %d)", p.Container), p.URL, "Published on your computer by Docker.", false)
			e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: http.MethodGet, Path: "/", URL: p.URL, Description: fmt.Sprintf("%s port %d", svc.Name, p.Container)})
			curls = append(curls, "curl -i "+shq(p.URL))
		} else {
			field(fmt.Sprintf("Host port (%d/%s)", p.Container, p.Protocol), hostAddr(p), "", false)
		}
	}
	if len(e.urls) > 0 {
		e.Kind = KindWebService
		e.Summary = "A container of your compose project serving HTTP on your computer."
		e.Actions = append(e.Actions, model.TryAction{ID: ActRequest, Label: "Send a request", Description: "Calls the service through its published port; change the path if you like.", Input: "http"})
		snippet("curl", "bash", strings.Join(curls, "\n\n"))
		snippet("JavaScript", "javascript", fmt.Sprintf("const res = await fetch(%q);\nconsole.log(res.status, await res.text());", e.urls[0]))
		snippet("Python", "python", fmt.Sprintf("import urllib.request\n\nwith urllib.request.urlopen(%q) as res:\n    print(res.status, res.read().decode())", e.urls[0]))
	} else {
		e.Kind = KindGeneric
		e.Summary = "A container of your compose project with ports published on your computer."
	}
	snippet("Open shell", "bash", "docker exec -it "+shq(container)+" sh")
	snippet("Logs", "bash", "docker logs -f "+shq(container))
	return e
}

func composeStateText(svc model.ComposeServiceStatus) string {
	s := string(svc.State)
	if svc.Health != "" {
		s += " (" + svc.Health + ")"
	}
	return s
}

// hostAddr is host:port of a published port; ports bound on every address
// or the loopback use 127.0.0.1.
func hostAddr(p model.ComposePort) string {
	host := p.HostIP
	if p.URL != "" || host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(p.Host))
}

func otherPorts(ports []model.ComposePort, main int, field func(label, value, hint string, secret bool)) {
	for _, p := range ports {
		if p.Container != main {
			field(fmt.Sprintf("Host port (%d/%s)", p.Container, p.Protocol), hostAddr(p), "", false)
		}
	}
}

// firstEnv returns the first non-empty value among keys.
func firstEnv(env map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := env[k]; v != "" {
			return v
		}
	}
	return ""
}

// dbFields adds connection fields and client snippets of a database or
// cache service, with the credentials of the container's environment
// (official images' variables).
func dbFields(e *composeEntry, kind string, port int, container string, env map[string]string,
	field func(label, value, hint string, secret bool), snippet func(label, lang, code string)) {
	host := "127.0.0.1"
	addr := host + ":" + strconv.Itoa(port)
	if port == 0 {
		e.Warning = "The database port is not published on your computer: add a ports entry to reach it from outside Docker."
	} else {
		field("Host", host, "", false)
		field("Port", strconv.Itoa(port), "", false)
	}
	exec := "docker exec -it " + shq(container) + " "
	switch kind {
	case "postgres":
		user := firstEnv(env, "POSTGRES_USER")
		if user == "" {
			user = "postgres"
		}
		pw := firstEnv(env, "POSTGRES_PASSWORD")
		db := firstEnv(env, "POSTGRES_DB")
		if db == "" {
			db = user
		}
		e.Summary = "A PostgreSQL database running in your compose project."
		field("User", user, "", false)
		field("Password", pw, "", true)
		field("Database", db, "", false)
		if port != 0 {
			uri := fmt.Sprintf("postgres://%s@%s/%s", userInfo(user, pw), addr, url.PathEscape(db))
			field("Connection URL", uri, "", pw != "")
			cmd := fmt.Sprintf("psql -h %s -p %d -U %s -d %s", host, port, shq(user), shq(db))
			if pw != "" {
				cmd = "PGPASSWORD=" + shq(pw) + " " + cmd
			}
			snippet("psql", "bash", cmd)
		}
		snippet("psql in the container", "bash", exec+"psql -U "+shq(user)+" -d "+shq(db))
	case "mysql":
		user := firstEnv(env, "MYSQL_USER", "MARIADB_USER")
		pw := firstEnv(env, "MYSQL_PASSWORD", "MARIADB_PASSWORD")
		if user == "" {
			user = "root"
			pw = firstEnv(env, "MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD")
		}
		db := firstEnv(env, "MYSQL_DATABASE", "MARIADB_DATABASE")
		e.Summary = "A MySQL/MariaDB database running in your compose project."
		field("User", user, "", false)
		field("Password", pw, "", true)
		field("Database", db, "", false)
		pwArg := ""
		if pw != "" {
			pwArg = " -p" + shq(pw)
		}
		dbArg := ""
		if db != "" {
			dbArg = " " + shq(db)
		}
		if port != 0 {
			field("Connection URL", fmt.Sprintf("mysql://%s@%s/%s", userInfo(user, pw), addr, url.PathEscape(db)), "", pw != "")
			snippet("mysql", "bash", fmt.Sprintf("mysql -h %s -P %d -u %s%s%s", host, port, shq(user), pwArg, dbArg))
		}
		snippet("mysql in the container", "bash", exec+"mysql -u "+shq(user)+pwArg+dbArg)
	case "mongo":
		user := firstEnv(env, "MONGO_INITDB_ROOT_USERNAME")
		pw := firstEnv(env, "MONGO_INITDB_ROOT_PASSWORD")
		e.Summary = "A MongoDB database running in your compose project."
		field("User", user, "", false)
		field("Password", pw, "", true)
		if port != 0 {
			uri := "mongodb://" + addr + "/"
			if user != "" {
				uri = "mongodb://" + userInfo(user, pw) + "@" + addr + "/?authSource=admin"
			}
			field("Connection URL", uri, "", pw != "")
			snippet("mongosh", "bash", "mongosh "+shq(uri))
		}
		snippet("mongosh in the container", "bash", exec+"mongosh")
	case "sqlserver":
		pw := firstEnv(env, "MSSQL_SA_PASSWORD", "SA_PASSWORD")
		e.Summary = "A SQL Server database running in your compose project."
		field("User", "sa", "", false)
		field("Password", pw, "", true)
		if port != 0 {
			snippet("sqlcmd", "bash", fmt.Sprintf("sqlcmd -S %s,%d -U sa -P %s -C", host, port, shq(pw)))
		}
	case "redis":
		pw := firstEnv(env, "REDIS_PASSWORD", "VALKEY_PASSWORD")
		e.Summary = "A Redis-compatible cache running in your compose project."
		field("Password", pw, "", true)
		auth := ""
		if pw != "" {
			auth = " -a " + shq(pw)
		}
		if port != 0 {
			uri := "redis://" + addr
			if pw != "" {
				uri = "redis://:" + url.QueryEscape(pw) + "@" + addr
			}
			field("Connection URL", uri, "", pw != "")
			snippet("redis-cli", "bash", fmt.Sprintf("redis-cli -h %s -p %d%s ping", host, port, auth))
		}
		snippet("redis-cli in the container", "bash", exec+"redis-cli"+auth)
	case "memcached":
		e.Summary = "A Memcached cache running in your compose project."
		if port != 0 {
			snippet("stats", "bash", fmt.Sprintf("printf 'stats\\r\\nquit\\r\\n' | nc %s %d", host, port))
		}
	}
}

func userInfo(user, pw string) string {
	if pw == "" {
		return url.PathEscape(user)
	}
	return url.UserPassword(user, pw).String()
}

// TryCompose runs a TryRequest against a compose service: only "request",
// to the project's published HTTP ports on this machine.
func TryCompose(ctx context.Context, rt sandbox.ComposeRuntime, req model.TryRequest) (model.TryResult, error) {
	entries := composeEntries(rt)
	i := slices.IndexFunc(entries, func(e composeEntry) bool { return e.NodeID == req.NodeID || e.Address == req.NodeID })
	if i < 0 {
		return model.TryResult{}, ErrNotFound
	}
	e := entries[i]
	if req.Action != ActRequest || len(e.urls) == 0 {
		return model.TryResult{}, badRequest("action %q is not available for %s", req.Action, e.Label)
	}
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	if req.Method == "" || req.Method == "ANY" {
		req.Method = http.MethodGet
	}
	if !validMethod[req.Method] {
		return model.TryResult{}, badRequest("unsupported method %q", req.Method)
	}
	target := strings.TrimSpace(req.Path)
	switch {
	case target == "":
		target = e.urls[0]
	case !strings.Contains(target, "://"):
		target = strings.TrimSuffix(e.urls[0], "/") + "/" + strings.TrimPrefix(target, "/")
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return model.TryResult{}, badRequest("invalid URL %q", target)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !loopback(u.Hostname()) || !composePort(entries, u.Port()) {
		return model.TryResult{}, badRequest("Try only calls the published HTTP ports of this compose project; use Connections for other services")
	}
	return doHTTP(ctx, req.Method, u.String(), req.Headers, req.Input), nil
}

func composePort(entries []composeEntry, port string) bool {
	for _, e := range entries {
		for _, raw := range e.urls {
			if u, err := url.Parse(raw); err == nil && u.Port() == port {
				return true
			}
		}
	}
	return false
}

// PostmanCompose is a collection with a GET per published HTTP port of the
// running services.
func PostmanCompose(wf *model.Workflow, rt sandbox.ComposeRuntime) PostmanCollection {
	name := rt.Status.WorkflowID
	if wf != nil && wf.Name != "" {
		name = wf.Name
	}
	col := PostmanCollection{
		Info:     PostmanInfo{Name: name + " (Nimbo compose)", Description: "Requests to the ports your compose project publishes on this computer.", Schema: PostmanSchema},
		Item:     []PostmanItem{},
		Variable: []PostmanVariable{},
	}
	for _, e := range composeEntries(rt) {
		var items []PostmanItem
		for _, raw := range e.urls {
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			items = append(items, PostmanItem{Name: "GET " + raw, Request: &PostmanRequest{
				Method: http.MethodGet, Header: []PostmanHeader{},
				URL: PostmanURL{Raw: raw, Protocol: u.Scheme, Host: strings.Split(u.Hostname(), "."), Port: u.Port(), Path: []string{""}},
			}})
		}
		if len(items) > 0 {
			col.Item = append(col.Item, PostmanItem{Name: e.Label, Item: items})
		}
	}
	return col
}
