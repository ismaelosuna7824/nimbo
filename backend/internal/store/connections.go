package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ErrConnectionNotFound is returned when a saved connection does not exist.
var ErrConnectionNotFound = errors.New("connection not found")

// ErrInvalidConnection wraps every validation failure of a connection.
// Messages never contain secret values.
var ErrInvalidConnection = errors.New("invalid connection")

const connectionsFile = "connections.json"

// Secret names accepted in Connection.ClearSecrets and reported in
// Connection.SecretsSet.
const (
	SecretPassword     = "password"
	SecretURI          = "uri"
	SecretAuthToken    = "auth.token"
	SecretAuthPassword = "auth.password"
)

var secretNames = []string{SecretAuthPassword, SecretAuthToken, SecretPassword, SecretURI}

// Connections is the global library of saved connections, persisted with
// their secrets in <home>/connections.json (mode 0600). Every read method
// except Connection returns masked copies. It is safe for concurrent use.
type Connections struct {
	path  string
	mu    sync.Mutex
	items map[string]model.Connection
}

// OpenConnections loads <home>/connections.json (a missing file is an empty
// library).
func OpenConnections(home string) (*Connections, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create home: %w", err)
	}
	c := &Connections{path: filepath.Join(home, connectionsFile), items: map[string]model.Connection{}}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var list []model.Connection
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.path, err)
	}
	for _, conn := range list {
		if validID.MatchString(conn.ID) {
			conn.SecretsSet, conn.ClearSecrets = nil, nil
			c.items[conn.ID] = conn
		}
	}
	return c, nil
}

// List returns every connection, masked, sorted by name.
func (c *Connections) List() []model.Connection {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.Connection, 0, len(c.items))
	for _, conn := range c.items {
		out = append(out, Mask(conn))
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one connection, masked.
func (c *Connections) Get(id string) (model.Connection, error) {
	conn, ok := c.Connection(id)
	if !ok {
		return model.Connection{}, ErrConnectionNotFound
	}
	return Mask(conn), nil
}

// Connection returns a connection with its secrets in clear. It implements
// loadtest.ConnectionResolver; never serialize its result to clients.
func (c *Connections) Connection(id string) (model.Connection, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, ok := c.items[id]
	if !ok {
		return model.Connection{}, false
	}
	return clone(conn), true
}

// Create validates and saves a new connection under a fresh ID and returns
// it masked. Any ID in the input is ignored.
func (c *Connections) Create(in model.Connection) (model.Connection, error) {
	conn := clone(in)
	conn.SecretsSet, conn.ClearSecrets = nil, nil
	if err := normalize(&conn); err != nil {
		return model.Connection{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		conn.ID = NewID(conn.Name)
		if _, taken := c.items[conn.ID]; !taken {
			break
		}
	}
	now := time.Now().UTC()
	conn.CreatedAt, conn.UpdatedAt = now, now
	if err := c.commit(conn.ID, &conn); err != nil {
		return model.Connection{}, err
	}
	return Mask(conn), nil
}

// Update replaces connection id with in. Blank secrets keep their stored
// values; secrets named in in.ClearSecrets are removed. Returns it masked.
func (c *Connections) Update(id string, in model.Connection) (model.Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stored, ok := c.items[id]
	if !ok {
		return model.Connection{}, ErrConnectionNotFound
	}
	conn, err := merge(stored, in)
	if err != nil {
		return model.Connection{}, err
	}
	conn.ID, conn.CreatedAt, conn.UpdatedAt = id, stored.CreatedAt, time.Now().UTC()
	if err := c.commit(id, &conn); err != nil {
		return model.Connection{}, err
	}
	return Mask(conn), nil
}

// Delete removes a connection.
func (c *Connections) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; !ok {
		return ErrConnectionNotFound
	}
	return c.commit(id, nil)
}

// Draft prepares an unsaved connection for testing: when it carries the ID
// of a saved connection, blank secrets are filled from the stored one (and
// ClearSecrets honored). The result is validated and has secrets in clear.
func (c *Connections) Draft(in model.Connection) (model.Connection, error) {
	if in.ID == "" {
		conn := clone(in)
		conn.SecretsSet, conn.ClearSecrets = nil, nil
		if err := normalize(&conn); err != nil {
			return model.Connection{}, err
		}
		return conn, nil
	}
	stored, ok := c.Connection(in.ID)
	if !ok {
		return model.Connection{}, ErrConnectionNotFound
	}
	conn, err := merge(stored, in)
	if err != nil {
		return model.Connection{}, err
	}
	conn.ID = in.ID
	return conn, nil
}

// commit applies a change (conn nil = delete) and persists the library; on
// failure the in-memory state is left unchanged. Caller holds c.mu.
func (c *Connections) commit(id string, conn *model.Connection) error {
	next := make(map[string]model.Connection, len(c.items)+1)
	for k, v := range c.items {
		next[k] = v
	}
	if conn == nil {
		delete(next, id)
	} else {
		next[id] = *conn
	}
	list := make([]model.Connection, 0, len(next))
	for _, v := range next {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(c.path, data); err != nil {
		return fmt.Errorf("save connections: %w", err)
	}
	c.items = next
	return nil
}

// writeFileAtomic writes data to path via a 0600 temp file and rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Mask returns a copy of conn without secret values; SecretsSet lists the
// secrets that are stored.
func Mask(conn model.Connection) model.Connection {
	out := clone(conn)
	out.ClearSecrets = nil
	out.SecretsSet = []string{}
	if out.Auth != nil {
		if out.Auth.Password != "" {
			out.SecretsSet = append(out.SecretsSet, SecretAuthPassword)
		}
		if out.Auth.Token != "" {
			out.SecretsSet = append(out.SecretsSet, SecretAuthToken)
		}
		out.Auth.Token, out.Auth.Password = "", ""
	}
	if out.Password != "" {
		out.SecretsSet = append(out.SecretsSet, SecretPassword)
	}
	if out.URI != "" {
		out.SecretsSet = append(out.SecretsSet, SecretURI)
	}
	out.Password, out.URI = "", ""
	if len(out.SecretsSet) == 0 {
		out.SecretsSet = nil
	}
	return out
}

// merge applies an update to stored: blank secrets keep the stored value,
// ClearSecrets removes them. The result is normalized and validated.
func merge(stored, in model.Connection) (model.Connection, error) {
	conn := clone(in)
	clear := conn.ClearSecrets
	conn.SecretsSet, conn.ClearSecrets = nil, nil
	for _, name := range clear {
		if !slices.Contains(secretNames, name) {
			return model.Connection{}, fmt.Errorf("%w: unknown secret %q in clearSecrets (want one of %s)",
				ErrInvalidConnection, name, strings.Join(secretNames, ", "))
		}
	}
	keep := func(name string, cur *string, old string) {
		if slices.Contains(clear, name) {
			*cur = ""
		} else if *cur == "" {
			*cur = old
		}
	}
	keep(SecretPassword, &conn.Password, stored.Password)
	keep(SecretURI, &conn.URI, stored.URI)
	if conn.Auth != nil {
		var old model.HTTPAuth
		if stored.Auth != nil {
			old = *stored.Auth
		}
		keep(SecretAuthToken, &conn.Auth.Token, old.Token)
		keep(SecretAuthPassword, &conn.Auth.Password, old.Password)
	}
	if err := normalize(&conn); err != nil {
		return model.Connection{}, err
	}
	return conn, nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConnection, fmt.Sprintf(format, args...))
}

// normalize trims fields, drops the ones that do not apply to the kind (so
// stale secrets of another kind are never kept) and validates the rest.
func normalize(c *model.Connection) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if c.Name == "" {
		return invalid("name is required")
	}
	if len(c.Name) > 120 {
		return invalid("name is longer than 120 characters")
	}
	http := c.Kind == model.ConnHTTP
	db := c.Kind == model.ConnPostgres || c.Kind == model.ConnMySQL || c.Kind == model.ConnRedis || c.Kind == model.ConnMongo
	aws := c.Kind == model.ConnAWS
	if !http && !db && !aws {
		return invalid("unknown kind %q (want http, postgres, mysql, redis, mongodb or aws)", c.Kind)
	}
	if !http {
		c.BaseURL, c.Headers, c.Auth, c.InsecureSkipVerify = "", nil, nil, false
	}
	if !db {
		c.URI, c.Host, c.Port, c.Username, c.Password, c.Database, c.TLS = "", "", 0, "", "", "", false
	}
	if !aws {
		c.Profile, c.Region = "", ""
	}
	switch {
	case http:
		return normalizeHTTP(c)
	case db:
		return normalizeDB(c)
	default:
		c.Profile = strings.TrimSpace(c.Profile)
		c.Region = strings.TrimSpace(c.Region)
		if strings.ContainsAny(c.Profile, " \t\r\n[]") {
			return invalid("profile %q is not a valid AWS profile name", c.Profile)
		}
		if c.Region != "" && !awsRegion(c.Region) {
			return invalid("region %q is not a valid AWS region (e.g. us-east-1)", c.Region)
		}
		return nil
	}
}

func awsRegion(r string) bool {
	if len(r) > 32 {
		return false
	}
	for _, ch := range r {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
			return false
		}
	}
	return strings.Count(r, "-") >= 2
}

func normalizeHTTP(c *model.Connection) error {
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	u, err := url.Parse(c.BaseURL)
	if c.BaseURL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("baseUrl must be an absolute http:// or https:// URL")
	}
	if u.User != nil {
		return invalid("baseUrl must not contain credentials; use auth instead")
	}
	for name, v := range c.Headers {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t\r\n:") {
			return invalid("header name %q is not valid", name)
		}
		if strings.ContainsAny(v, "\r\n") {
			return invalid("header %s contains a line break", name)
		}
	}
	if len(c.Headers) == 0 {
		c.Headers = nil
	}
	if c.Auth == nil {
		return nil
	}
	a := c.Auth
	a.Type = strings.TrimSpace(a.Type)
	switch a.Type {
	case "", "none":
		c.Auth = nil
	case "bearer":
		a.Username, a.Password, a.Header = "", "", ""
		if a.Token == "" {
			return invalid("auth.token is required for bearer auth")
		}
	case "api_key":
		a.Username, a.Password = "", ""
		a.Header = strings.TrimSpace(a.Header)
		if a.Header == "" {
			a.Header = "X-API-Key"
		}
		if strings.ContainsAny(a.Header, " \t\r\n:") {
			return invalid("auth.header %q is not a valid header name", a.Header)
		}
		if a.Token == "" {
			return invalid("auth.token is required for api_key auth")
		}
	case "basic":
		a.Token, a.Header = "", ""
		if a.Username == "" {
			return invalid("auth.username is required for basic auth")
		}
	default:
		return invalid("unknown auth.type %q (want none, bearer, basic or api_key)", a.Type)
	}
	return nil
}

var uriSchemes = map[model.ConnectionKind][]string{
	model.ConnPostgres: {"postgres", "postgresql"},
	model.ConnMySQL:    {"mysql"},
	model.ConnRedis:    {"redis", "rediss"},
	model.ConnMongo:    {"mongodb", "mongodb+srv"},
}

func normalizeDB(c *model.Connection) error {
	c.URI = strings.TrimSpace(c.URI)
	c.Host = strings.TrimSpace(c.Host)
	c.Database = strings.TrimSpace(c.Database)
	if c.URI == "" && c.Host == "" {
		return invalid("%s connections need a uri or a host", c.Kind)
	}
	if c.URI != "" {
		// The URI is a secret: errors describe it without quoting it.
		scheme, _, found := strings.Cut(c.URI, "://")
		mysqlDSN := c.Kind == model.ConnMySQL && !found && strings.Contains(c.URI, "@")
		if !mysqlDSN && (!found || !slices.Contains(uriSchemes[c.Kind], strings.ToLower(scheme))) {
			return invalid("uri must start with %s://", strings.Join(uriSchemes[c.Kind], ":// or "))
		}
	}
	if strings.ContainsAny(c.Host, " /\t\r\n") {
		return invalid("host %q is not a valid host name", c.Host)
	}
	if c.Port < 0 || c.Port > 65535 {
		return invalid("port %d is out of range", c.Port)
	}
	if c.Kind == model.ConnRedis && c.Database != "" {
		n := 0
		if _, err := fmt.Sscanf(c.Database, "%d", &n); err != nil || n < 0 || fmt.Sprint(n) != c.Database {
			return invalid("redis database must be a number")
		}
	}
	return nil
}

func clone(c model.Connection) model.Connection {
	out := c
	if c.Headers != nil {
		out.Headers = make(map[string]string, len(c.Headers))
		for k, v := range c.Headers {
			out.Headers[k] = v
		}
	}
	if c.Auth != nil {
		a := *c.Auth
		out.Auth = &a
	}
	out.SecretsSet = slices.Clone(c.SecretsSet)
	out.ClearSecrets = slices.Clone(c.ClearSecrets)
	return out
}
