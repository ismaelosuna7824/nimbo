package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func openConns(t *testing.T) (*Connections, string) {
	t.Helper()
	home := t.TempDir()
	c, err := OpenConnections(home)
	if err != nil {
		t.Fatal(err)
	}
	return c, home
}

func TestConnectionCreateMasksAndPersists0600(t *testing.T) {
	c, home := openConns(t)
	out, err := c.Create(model.Connection{
		Name: "Orders DB", Kind: model.ConnPostgres,
		Host: "db.internal", Port: 5432, Username: "app", Password: "hunter2", Database: "orders",
		BaseURL: "https://ignored.example", // not an http connection: dropped
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.ID, "orders-db-") || out.Password != "" || out.BaseURL != "" {
		t.Fatalf("unexpected masked output: %+v", out)
	}
	if !slices.Equal(out.SecretsSet, []string{SecretPassword}) {
		t.Fatalf("secretsSet = %v", out.SecretsSet)
	}

	path := filepath.Join(home, connectionsFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("connections.json mode = %o, want 600", perm)
	}

	reopened, err := OpenConnections(home)
	if err != nil {
		t.Fatal(err)
	}
	clear, ok := reopened.Connection(out.ID)
	if !ok || clear.Password != "hunter2" || clear.Host != "db.internal" {
		t.Fatalf("resolver lost data after reopen: %+v", clear)
	}
	listed := reopened.List()
	if len(listed) != 1 || listed[0].Password != "" {
		t.Fatalf("List leaked or lost entries: %+v", listed)
	}
}

func TestConnectionUpdateKeepsBlankAndClearsNamedSecrets(t *testing.T) {
	c, _ := openConns(t)
	created, err := c.Create(model.Connection{
		Name: "API", Kind: model.ConnHTTP, BaseURL: "https://api.example.com",
		Auth: &model.HTTPAuth{Type: "bearer", Token: "tok-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The masked object sent back unchanged keeps the token.
	created.Description = "renamed"
	upd, err := c.Update(created.ID, created)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(upd.SecretsSet, SecretAuthToken) || upd.Auth.Token != "" {
		t.Fatalf("token not kept/masked: %+v", upd)
	}
	if clear, _ := c.Connection(created.ID); clear.Auth.Token != "tok-1" || clear.Description != "renamed" {
		t.Fatalf("stored = %+v", clear)
	}

	// A new value replaces it.
	upd.Auth.Token = "tok-2"
	if _, err := c.Update(created.ID, upd); err != nil {
		t.Fatal(err)
	}
	if clear, _ := c.Connection(created.ID); clear.Auth.Token != "tok-2" {
		t.Fatalf("token not replaced: %+v", clear.Auth)
	}

	// Clearing the token of bearer auth is invalid; switching to none with
	// clearSecrets drops it.
	upd.ClearSecrets = []string{SecretAuthToken}
	if _, err := c.Update(created.ID, upd); !errors.Is(err, ErrInvalidConnection) {
		t.Fatalf("clearing a required token: err = %v", err)
	}
	upd.Auth = &model.HTTPAuth{Type: "none"}
	out, err := c.Update(created.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if clear, _ := c.Connection(created.ID); clear.Auth != nil || len(out.SecretsSet) != 0 {
		t.Fatalf("auth not cleared: %+v / %+v", clear.Auth, out.SecretsSet)
	}

	if _, err := c.Update(created.ID, model.Connection{Name: "x", Kind: model.ConnAWS, ClearSecrets: []string{"apiKey"}}); !errors.Is(err, ErrInvalidConnection) {
		t.Fatalf("unknown clearSecrets name accepted: %v", err)
	}
	if _, err := c.Update("nope-aaaaaa", upd); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("missing id: err = %v", err)
	}
}

func TestConnectionClearDatabaseSecret(t *testing.T) {
	c, _ := openConns(t)
	created, err := c.Create(model.Connection{Name: "cache", Kind: model.ConnRedis, URI: "redis://:pw@cache:6379/0"})
	if err != nil {
		t.Fatal(err)
	}
	// Clearing the URI leaves no host: invalid. Supplying a host makes it valid.
	created.ClearSecrets = []string{SecretURI}
	if _, err := c.Update(created.ID, created); !errors.Is(err, ErrInvalidConnection) {
		t.Fatalf("err = %v", err)
	}
	created.Host = "cache"
	out, err := c.Update(created.ID, created)
	if err != nil {
		t.Fatal(err)
	}
	if clear, _ := c.Connection(created.ID); clear.URI != "" || len(out.SecretsSet) != 0 {
		t.Fatalf("uri not cleared: %+v", clear)
	}
}

func TestConnectionValidation(t *testing.T) {
	c, _ := openConns(t)
	cases := map[string]model.Connection{
		"name required":       {Kind: model.ConnHTTP, BaseURL: "https://x.example"},
		"unknown kind":        {Name: "x", Kind: "ftp"},
		"relative base url":   {Name: "x", Kind: model.ConnHTTP, BaseURL: "/api"},
		"non-http scheme":     {Name: "x", Kind: model.ConnHTTP, BaseURL: "ftp://x.example"},
		"credentials in url":  {Name: "x", Kind: model.ConnHTTP, BaseURL: "https://u:p@x.example"},
		"bad auth type":       {Name: "x", Kind: model.ConnHTTP, BaseURL: "https://x.example", Auth: &model.HTTPAuth{Type: "oauth"}},
		"bearer without tok":  {Name: "x", Kind: model.ConnHTTP, BaseURL: "https://x.example", Auth: &model.HTTPAuth{Type: "bearer"}},
		"db without target":   {Name: "x", Kind: model.ConnPostgres, Username: "u"},
		"wrong uri scheme":    {Name: "x", Kind: model.ConnMongo, URI: "postgres://secret-pw@h/db"},
		"port out of range":   {Name: "x", Kind: model.ConnMySQL, Host: "h", Port: 70000},
		"redis db not number": {Name: "x", Kind: model.ConnRedis, Host: "h", Database: "zero"},
		"bad aws region":      {Name: "x", Kind: model.ConnAWS, Region: "Mars"},
	}
	for name, in := range cases {
		_, err := c.Create(in)
		if !errors.Is(err, ErrInvalidConnection) {
			t.Errorf("%s: err = %v, want ErrInvalidConnection", name, err)
			continue
		}
		if strings.Contains(err.Error(), "secret-pw") {
			t.Errorf("%s: error echoes the secret URI: %v", name, err)
		}
	}
	if len(c.List()) != 0 {
		t.Fatal("invalid connections were saved")
	}
	for _, ok := range []model.Connection{
		{Name: "aws", Kind: model.ConnAWS},
		{Name: "my", Kind: model.ConnMySQL, URI: "app:pw@tcp(db:3306)/shop"},
		{Name: "api", Kind: model.ConnHTTP, BaseURL: "http://127.0.0.1:8080", Auth: &model.HTTPAuth{Type: "api_key", Token: "k"}},
	} {
		out, err := c.Create(ok)
		if err != nil {
			t.Errorf("%s: %v", ok.Name, err)
			continue
		}
		if ok.Kind == model.ConnHTTP && out.Auth.Header != "X-API-Key" {
			t.Errorf("api_key header default not applied: %+v", out.Auth)
		}
	}
}

func TestConnectionDraftFillsStoredSecrets(t *testing.T) {
	c, _ := openConns(t)
	created, err := c.Create(model.Connection{Name: "pg", Kind: model.ConnPostgres, Host: "h", Username: "u", Password: "stored-pw"})
	if err != nil {
		t.Fatal(err)
	}
	draft := created // masked: blank password
	draft.Host = "h2"
	got, err := c.Draft(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "stored-pw" || got.Host != "h2" {
		t.Fatalf("draft = %+v", got)
	}
	if clear, _ := c.Connection(created.ID); clear.Host != "h" {
		t.Fatal("draft modified the stored connection")
	}
	if _, err := c.Draft(model.Connection{ID: "missing-aaaaaa", Name: "x", Kind: model.ConnAWS}); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("err = %v", err)
	}
	fresh, err := c.Draft(model.Connection{Name: "new", Kind: model.ConnRedis, Host: "r", Password: "p"})
	if err != nil || fresh.Password != "p" {
		t.Fatalf("unsaved draft: %+v, %v", fresh, err)
	}
}

func TestConnectionDelete(t *testing.T) {
	c, home := openConns(t)
	created, err := c.Create(model.Connection{Name: "aws", Kind: model.ConnAWS, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(created.ID); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	reopened, err := OpenConnections(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List()) != 0 {
		t.Fatal("deleted connection persisted")
	}
}
