package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// Connections library: saved, already-deployed services (any HTTP API,
// PostgreSQL, MySQL, Redis, MongoDB, a real AWS account) that can be
// load-tested without a workflow. Tool results never contain secrets.

// externalWarning is appended to the descriptions of tools that send
// traffic to connections.
const externalWarning = " Traffic to a connection leaves the local sandbox and hits a REAL service (or a real AWS account, which may cost money): before calling this, ask the user for explicit approval naming the service, target and load profile. Write targets (POST/PUT, INSERT/UPDATE, SET, insertOne, SendMessage, PutItem, PutObject, ...) create real data."

// connection resolves ref as a connection ID, falling back to a
// case-insensitive name match. The result is masked by the daemon.
func (c *client) connection(ctx context.Context, ref string) (*model.Connection, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("connection is required (ID or name; see list_connections)")
	}
	var conn model.Connection
	err := c.get(ctx, "/connections/"+pathEscape(ref), &conn)
	if err == nil {
		return &conn, nil
	}
	if !isNotFound(err) {
		return nil, err
	}
	var all []model.Connection
	if err := c.get(ctx, "/connections", &all); err != nil {
		return nil, err
	}
	var match *model.Connection
	for i := range all {
		if strings.EqualFold(all[i].Name, ref) {
			if match != nil {
				return nil, fmt.Errorf("several connections are named %q; use the ID", ref)
			}
			match = &all[i]
		}
	}
	if match == nil {
		return nil, fmt.Errorf("connection %q not found (see list_connections)", ref)
	}
	return match, nil
}

// sensitiveHeader matches header names whose values are credentials.
var sensitiveHeader = regexp.MustCompile(`(?i)auth|token|key|secret|cookie|passw|session|signature`)

// connectionView is a connection as shown to agents: secrets blanked (and
// listed in secretsSet), credential-looking header values redacted. The
// daemon already masks responses; masking again guards against any value
// slipping through while keeping the daemon's secretsSet.
func connectionView(conn model.Connection) model.Connection {
	v := store.Mask(conn)
	for _, name := range conn.SecretsSet {
		if !slices.Contains(v.SecretsSet, name) {
			v.SecretsSet = append(v.SecretsSet, name)
		}
	}
	for name := range v.Headers {
		if sensitiveHeader.MatchString(name) {
			v.Headers[name] = "[redacted]"
		}
	}
	return v
}

func (t *tools) listConnections(ctx context.Context, _ struct{}) (any, error) {
	var all []model.Connection
	if err := t.c.get(ctx, "/connections", &all); err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return "No saved connections. Create one with create_connection.", nil
	}
	out := make([]model.Connection, len(all))
	for i, conn := range all {
		out[i] = connectionView(conn)
	}
	return out, nil
}

type connectionRefIn struct {
	Connection string `json:"connection" jsonschema:"connection ID (preferred) or exact name"`
}

func (t *tools) getConnection(ctx context.Context, in connectionRefIn) (any, error) {
	conn, err := t.c.connection(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	return connectionView(*conn), nil
}

// connectionFields are the editable fields of a connection; nil leaves a
// field unchanged on update.
type connectionFields struct {
	Name               *string           `json:"name,omitempty" jsonschema:"display name"`
	Kind               *string           `json:"kind,omitempty" jsonschema:"http, postgres, mysql, redis, mongodb or aws"`
	Description        *string           `json:"description,omitempty"`
	BaseURL            *string           `json:"base_url,omitempty" jsonschema:"http: absolute http(s) base URL, e.g. https://api.example.com/v1 (no credentials in the URL)"`
	Headers            map[string]string `json:"headers,omitempty" jsonschema:"http: headers sent with every request (replaces the stored set)"`
	Auth               *model.HTTPAuth   `json:"auth,omitempty" jsonschema:"http: {type: none|bearer|basic|api_key, token?, username?, password?, header? (api_key header, default X-API-Key)}; a blank token/password keeps the stored one"`
	InsecureSkipVerify *bool             `json:"insecure_skip_verify,omitempty" jsonschema:"http: skip TLS certificate verification"`
	URI                *string           `json:"uri,omitempty" jsonschema:"databases: full connection string (secret), e.g. postgres://user:pass@host:5432/db; alternative to host/port/..."`
	Host               *string           `json:"host,omitempty" jsonschema:"databases: host name"`
	Port               *int              `json:"port,omitempty" jsonschema:"databases: port (default per engine)"`
	Username           *string           `json:"username,omitempty" jsonschema:"databases: user"`
	Password           *string           `json:"password,omitempty" jsonschema:"databases: password (secret); blank keeps the stored one"`
	Database           *string           `json:"database,omitempty" jsonschema:"databases: database name (redis: DB number)"`
	TLS                *bool             `json:"tls,omitempty" jsonschema:"databases: use TLS"`
	Profile            *string           `json:"profile,omitempty" jsonschema:"aws: shared config/SSO profile name (empty = default credential chain)"`
	Region             *string           `json:"region,omitempty" jsonschema:"aws: region, e.g. us-east-1 (recommended)"`
}

func (f *connectionFields) empty() bool {
	data, _ := json.Marshal(f)
	return string(data) == "{}"
}

func (f *connectionFields) apply(c *model.Connection) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&c.Name, f.Name)
	if f.Kind != nil {
		c.Kind = model.ConnectionKind(*f.Kind)
	}
	set(&c.Description, f.Description)
	set(&c.BaseURL, f.BaseURL)
	if f.Headers != nil {
		c.Headers = f.Headers
	}
	if f.Auth != nil {
		a := *f.Auth
		c.Auth = &a
	}
	if f.InsecureSkipVerify != nil {
		c.InsecureSkipVerify = *f.InsecureSkipVerify
	}
	set(&c.URI, f.URI)
	set(&c.Host, f.Host)
	if f.Port != nil {
		c.Port = *f.Port
	}
	set(&c.Username, f.Username)
	set(&c.Password, f.Password)
	set(&c.Database, f.Database)
	if f.TLS != nil {
		c.TLS = *f.TLS
	}
	set(&c.Profile, f.Profile)
	set(&c.Region, f.Region)
}

func (t *tools) createConnection(ctx context.Context, in connectionFields) (any, error) {
	var conn model.Connection
	in.apply(&conn)
	var out model.Connection
	if err := t.c.do(ctx, http.MethodPost, "/connections", conn, &out); err != nil {
		return nil, err
	}
	return connectionView(out), nil
}

type updateConnectionIn struct {
	Connection string `json:"connection" jsonschema:"connection ID or exact name"`
	connectionFields
	ClearSecrets []string `json:"clear_secrets,omitempty" jsonschema:"stored secrets to remove: password, uri, auth.token, auth.password"`
}

func (t *tools) updateConnection(ctx context.Context, in updateConnectionIn) (any, error) {
	conn, err := t.c.connection(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	// conn is masked: its blank secrets keep the stored values on PUT.
	in.apply(conn)
	conn.SecretsSet = nil
	conn.ClearSecrets = in.ClearSecrets
	var out model.Connection
	if err := t.c.do(ctx, http.MethodPut, "/connections/"+pathEscape(conn.ID), conn, &out); err != nil {
		return nil, err
	}
	return connectionView(out), nil
}

func (t *tools) deleteConnection(ctx context.Context, in connectionRefIn) (any, error) {
	conn, err := t.c.connection(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	if err := t.c.do(ctx, http.MethodDelete, "/connections/"+pathEscape(conn.ID), nil, nil); err != nil {
		return nil, err
	}
	return fmt.Sprintf("Deleted connection %s (%s) and its load test history.", conn.Name, conn.ID), nil
}

type testConnectionIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"saved connection ID or name; omit to test an unsaved draft described by the other fields. With both, the fields override the saved connection (blank secrets come from it)"`
	connectionFields
}

func (t *tools) testConnection(ctx context.Context, in testConnectionIn) (any, error) {
	var check model.ConnectionCheck
	if in.Connection == "" {
		var draft model.Connection
		in.apply(&draft)
		if err := t.c.do(ctx, http.MethodPost, "/connections/test", draft, &check); err != nil {
			return nil, err
		}
		return check, nil
	}
	conn, err := t.c.connection(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	if in.connectionFields.empty() {
		err = t.c.do(ctx, http.MethodPost, "/connections/"+pathEscape(conn.ID)+"/test", nil, &check)
	} else {
		in.apply(conn)
		conn.SecretsSet = nil
		err = t.c.do(ctx, http.MethodPost, "/connections/test", conn, &check)
	}
	if err != nil {
		return nil, err
	}
	return check, nil
}

func (t *tools) connectionTargets(ctx context.Context, connID string) ([]model.LoadTarget, error) {
	var targets []model.LoadTarget
	err := t.c.get(ctx, "/connections/"+pathEscape(connID)+"/targets", &targets)
	return targets, err
}

func (t *tools) listConnectionTargets(ctx context.Context, in connectionRefIn) (any, error) {
	conn, err := t.c.connection(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	targets, err := t.connectionTargets(ctx, conn.ID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return "No suggested targets; pass a target object to run_connection_load_test.", nil
	}
	type item struct {
		Index int `json:"index"`
		model.LoadTarget
	}
	out := make([]item, len(targets))
	for i, tg := range targets {
		out[i] = item{i, tg}
	}
	return out, nil
}

type connectionLoadTestIn struct {
	Connection      string            `json:"connection" jsonschema:"connection ID or exact name"`
	Target          any               `json:"target" jsonschema:"index from list_connection_targets, or a target object {kind: http|aws|sql|redis|mongo, label, http: {method, url (absolute or a path relative to the connection's base URL), headers?, body?} | aws: {operation, resource, payload?, key?} | sql: {query, args? (bound as parameters), mode: query|exec} | redis: {command, e.g. \"GET bench:{{rand}}\"} | mongo: {database, collection?, operation: insertOne|findOne|find|updateOne|deleteOne|runCommand, document?, update?}}"`
	Preset          string            `json:"preset,omitempty" jsonschema:"smoke (default), load, stress, spike or soak"`
	Stages          []model.LoadStage `json:"stages,omitempty" jsonschema:"custom ramp stages overriding the preset; each ramps linearly to target over durationSec"`
	Mode            string            `json:"mode,omitempty" jsonschema:"rps (target = requests/second) or concurrency (target = virtual users)"`
	Thresholds      []model.Threshold `json:"thresholds,omitempty" jsonschema:"pass/fail criteria, e.g. {metric: p95_ms, op: <, value: 200}; metrics p50_ms p90_ms p95_ms p99_ms max_ms error_rate rps_min"`
	ConfirmExternal bool              `json:"confirm_external,omitempty" jsonschema:"must be true: set it only after the user explicitly approved this load test against the real service"`
	Wait            *bool             `json:"wait,omitempty" jsonschema:"wait for the result (default true)"`
}

func (t *tools) runConnectionLoadTest(ctx context.Context, in connectionLoadTestIn) (any, error) {
	if !in.ConfirmExternal {
		return nil, fmt.Errorf("confirm_external must be true: this load test sends traffic to a real service outside the local sandbox. Ask the user for explicit approval (service, target, load profile; write targets create real data), then retry with confirm_external=true")
	}
	conn, err := t.c.connection(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	target, err := t.resolveTarget(ctx, in.Target, func(ctx context.Context) ([]model.LoadTarget, error) {
		return t.connectionTargets(ctx, conn.ID)
	})
	if err != nil {
		return nil, err
	}
	if target.ConnectionID == "" {
		target.ConnectionID = conn.ID
	}
	if target.ConnectionID != conn.ID {
		return nil, fmt.Errorf("target.connectionId %q does not match connection %s", target.ConnectionID, conn.ID)
	}
	spec, err := t.loadSpec(ctx, target, in.Preset, in.Stages, in.Mode, in.Thresholds)
	if err != nil {
		return nil, err
	}
	spec.ConfirmExternal = true
	return t.startLoadRun(ctx, "/connections/"+pathEscape(conn.ID)+"/loadtests", spec, in.Wait)
}
