package loadtest

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// plan is a target resolved before anything is sent: its connection (nil
// for sandbox targets), the effective HTTP URL and whether traffic leaves the
// local sandbox.
type plan struct {
	conn     *model.Connection
	url      string
	external bool
}

// plan resolves the target's connection and decides whether it is external.
// It sends nothing; ConfirmExternal is enforced by the caller.
func (e *Engine) plan(wfID string, spec *model.LoadTestSpec) (plan, error) {
	t := &spec.Target
	var p plan
	if t.ConnectionID != "" {
		if e.connections == nil {
			return p, errors.New("saved connections are not available")
		}
		c, ok := e.connections.Connection(t.ConnectionID)
		if !safeName(t.ConnectionID) || !ok {
			return p, fmt.Errorf("%w: %q", ErrConnectionNotFound, t.ConnectionID)
		}
		if !connectionServes(c.Kind, t.Kind) {
			return p, fmt.Errorf("connection %q (%s) cannot run %s targets", c.Name, c.Kind, t.Kind)
		}
		p.conn = &c
		p.external = true
	}
	switch t.Kind {
	case model.TargetHTTP:
		if t.HTTP == nil {
			return p, errors.New("http target needs a URL")
		}
		raw := strings.TrimSpace(t.HTTP.URL)
		if p.conn != nil {
			u, err := joinURL(p.conn.BaseURL, raw)
			if err != nil {
				return p, err
			}
			p.url = u
			return p, nil
		}
		if raw == "" {
			return p, errors.New("http target needs a URL")
		}
		p.url = raw
		if e.resolver != nil {
			p.url = e.resolver.HostURL(wfID, raw)
		}
		u, err := url.Parse(p.url)
		if err != nil {
			return p, fmt.Errorf("invalid URL %q: %w", raw, err)
		}
		p.external = !e.sandboxHost(wfID, u.Hostname())
	case model.TargetAWS:
		if t.AWS == nil {
			return p, errors.New("aws target needs an operation and a resource")
		}
	case model.TargetSQL, model.TargetRedis, model.TargetMongo:
		if p.conn == nil {
			return p, fmt.Errorf("%s targets need a saved connection (connectionId)", t.Kind)
		}
		if (t.Kind == model.TargetSQL && t.SQL == nil) || (t.Kind == model.TargetRedis && t.Redis == nil) || (t.Kind == model.TargetMongo && t.Mongo == nil) {
			return p, fmt.Errorf("%s target is missing its %s settings", t.Kind, t.Kind)
		}
	default:
		return p, fmt.Errorf("unknown target kind %q", t.Kind)
	}
	return p, nil
}

// connectionServes reports whether a connection kind can run a target kind.
func connectionServes(c model.ConnectionKind, t model.LoadTargetKind) bool {
	switch c {
	case model.ConnHTTP:
		return t == model.TargetHTTP
	case model.ConnAWS:
		return t == model.TargetAWS
	case model.ConnPostgres, model.ConnMySQL:
		return t == model.TargetSQL
	case model.ConnRedis:
		return t == model.TargetRedis
	case model.ConnMongo:
		return t == model.TargetMongo
	}
	return false
}

// sandboxHost reports whether host is local: loopback, localhost and its
// subdomains, Floci's *.localhost.floci.io (resolves to 127.0.0.1), or the
// host of one of the workflow's sandbox emulators.
func (e *Engine) sandboxHost(wfID, host string) bool {
	if isLocalHost(host) {
		return true
	}
	if e.resolver == nil || wfID == "" {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, cloud := range []model.Cloud{model.CloudAWS, model.CloudGCP, model.CloudAzure} {
		if base, ok := e.resolver.EmulatorURL(wfID, cloud); ok {
			if u, err := url.Parse(base); err == nil && strings.EqualFold(u.Hostname(), host) {
				return true
			}
		}
	}
	return false
}

func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch {
	case host == "":
		return false
	case host == "localhost", strings.HasSuffix(host, ".localhost"):
		return true
	case host == "localhost.floci.io", strings.HasSuffix(host, ".localhost.floci.io"):
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// joinURL resolves a connection target URL: absolute URLs are kept, paths
// are appended to the base URL ("https://h/v1" + "/users" =
// "https://h/v1/users"), "" is the base URL itself.
func joinURL(base, p string) (string, error) {
	if strings.Contains(p, "://") {
		return p, nil
	}
	if base == "" {
		return "", errors.New("the connection has no base URL; use an absolute URL")
	}
	switch {
	case p == "":
		return base, nil
	case strings.HasPrefix(p, "?"):
		return base + p, nil
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(p, "/"), nil
}

// connHeaders returns the connection's headers with its authentication.
func connHeaders(c *model.Connection) http.Header {
	h := http.Header{}
	for k, v := range c.Headers {
		h.Set(k, v)
	}
	if a := c.Auth; a != nil {
		switch a.Type {
		case "bearer":
			h.Set("Authorization", "Bearer "+a.Token)
		case "basic":
			h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password)))
		case "api_key":
			h.Set(orDefault(a.Header, "X-API-Key"), a.Token)
		}
	}
	return h
}

// Patterns of credentials embedded in connection strings: URL userinfo,
// MySQL DSNs (user:pass@tcp(...)) and key=value DSNs (password=...).
var (
	urlPassword   = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@\s]*:)[^@\s/]*@`)
	mysqlPassword = regexp.MustCompile(`([^\s:/@]+:)[^@\s]*@((?:tcp|unix)\()`)
	kvPassword    = regexp.MustCompile(`(?i)\b(password|passwd|pwd)=('[^']*'|\S*)`)
)

const redacted = "****"

// redactSecrets removes the connection's secrets (and any credentials found
// in connection strings) from s.
func redactSecrets(s string, c *model.Connection) string {
	s = urlPassword.ReplaceAllString(s, "${1}"+redacted+"@")
	s = mysqlPassword.ReplaceAllString(s, "${1}"+redacted+"@${2}")
	s = kvPassword.ReplaceAllString(s, "${1}="+redacted)
	if c == nil {
		return s
	}
	secrets := []string{c.URI, c.Password}
	if u, err := url.Parse(c.URI); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			secrets = append(secrets, pw)
		}
	}
	if c.Auth != nil {
		secrets = append(secrets, c.Auth.Token, c.Auth.Password)
		if c.Auth.Type == "basic" {
			secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(c.Auth.Username+":"+c.Auth.Password)))
		}
	}
	for _, sec := range secrets {
		// Very short secrets would mangle unrelated text; the patterns
		// above already cover them inside connection strings.
		if len(sec) < 3 {
			continue
		}
		for _, form := range []string{sec, url.QueryEscape(sec), url.PathEscape(sec), strconv.Quote(sec)[1 : len(strconv.Quote(sec))-1]} {
			s = strings.ReplaceAll(s, form, redacted)
		}
	}
	return s
}

// hostPort joins a connection's host and port, applying the default port.
func hostPort(c *model.Connection, def int) (string, error) {
	if c.Host == "" {
		return "", errors.New("the connection needs a URI or a host")
	}
	port := c.Port
	if port == 0 {
		port = def
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port)), nil
}
