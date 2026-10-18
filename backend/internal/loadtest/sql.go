package loadtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// dialTimeout bounds connection establishment to databases.
const dialTimeout = 10 * time.Second

// sqlExec runs one statement per request on PostgreSQL (pgx pool) or MySQL
// (database/sql). Args are rendered per request and always sent as bound
// parameters, never spliced into the statement text.
type sqlExec struct {
	query    string
	args     []template
	readRows bool
	pg       *pgxpool.Pool
	db       *sql.DB
}

func newSQLExec(t *model.SQLTarget, c model.Connection, pool int) (*sqlExec, error) {
	s := &sqlExec{query: strings.TrimSpace(t.Query)}
	if s.query == "" {
		return nil, errors.New("sql target needs a query")
	}
	switch t.Mode {
	case "", "query":
		s.readRows = true
	case "exec":
	default:
		return nil, fmt.Errorf("unknown sql mode %q (query or exec)", t.Mode)
	}
	for _, a := range t.Args {
		s.args = append(s.args, compileTemplate(a))
	}
	switch c.Kind {
	case model.ConnPostgres:
		cfg, err := postgresConfig(c)
		if err != nil {
			return nil, err
		}
		cfg.MaxConns = int32(max(pool, 1))
		// The pool connects lazily: nothing is sent before preflight.
		if s.pg, err = pgxpool.NewWithConfig(context.Background(), cfg); err != nil {
			return nil, err
		}
	case model.ConnMySQL:
		cfg, err := mysqlConfig(c)
		if err != nil {
			return nil, err
		}
		conn, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		s.db = sql.OpenDB(conn)
		s.db.SetMaxOpenConns(max(pool, 1))
		s.db.SetMaxIdleConns(max(pool, 1))
	default:
		return nil, fmt.Errorf("sql targets need a postgres or mysql connection, not %s", c.Kind)
	}
	return s, nil
}

// bind renders the arguments of request seq.
func (s *sqlExec) bind(seq int64) []any {
	args := make([]any, len(s.args))
	for i, a := range s.args {
		args[i] = a.render(seq)
	}
	return args
}

func (s *sqlExec) preflight(ctx context.Context) error {
	if _, err := s.run(ctx, 0); err != nil {
		return fmt.Errorf("%s (%s): %w", s.query, classifySQL(err), err)
	}
	return nil
}

func (s *sqlExec) do(ctx context.Context, seq int64) outcome {
	n, err := s.run(ctx, seq)
	o := outcome{bytes: n}
	if err != nil {
		o.errKind = classifySQL(err)
	}
	return o
}

// run executes the statement; in query mode it reads every row and returns
// the raw size of the values read.
func (s *sqlExec) run(ctx context.Context, seq int64) (int64, error) {
	args := s.bind(seq)
	if s.pg != nil {
		if !s.readRows {
			_, err := s.pg.Exec(ctx, s.query, args...)
			return 0, err
		}
		rows, err := s.pg.Query(ctx, s.query, args...)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		var n int64
		for rows.Next() {
			for _, v := range rows.RawValues() {
				n += int64(len(v))
			}
		}
		return n, rows.Err()
	}
	if !s.readRows {
		_, err := s.db.ExecContext(ctx, s.query, args...)
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, s.query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for _, v := range vals {
			n += int64(len(v))
		}
	}
	return n, rows.Err()
}

func (s *sqlExec) close() {
	if s.pg != nil {
		s.pg.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
}

// classifySQL maps database errors to error kinds: sql:<SQLSTATE> for
// PostgreSQL, mysql:<errno> for MySQL, auth_failed for rejected credentials.
func classifySQL(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		if pe.Code == "28P01" || pe.Code == "28000" {
			return errAuthFailed
		}
		return "sql:" + pe.Code
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		if me.Number == 1045 || me.Number == 1698 {
			return errAuthFailed
		}
		return "mysql:" + strconv.Itoa(int(me.Number))
	}
	if pgconn.Timeout(err) {
		return errTimeout
	}
	return classify(err)
}

// postgresConfig builds the pool config from the connection's URI (any
// libpq connection string) or its fields; TLS selects sslmode=require.
func postgresConfig(c model.Connection) (*pgxpool.Config, error) {
	dsn := c.URI
	if dsn == "" {
		addr, err := hostPort(&c, 5432)
		if err != nil {
			return nil, err
		}
		u := url.URL{Scheme: "postgres", Host: addr, Path: "/" + c.Database}
		switch {
		case c.Username != "" && c.Password != "":
			u.User = url.UserPassword(c.Username, c.Password)
		case c.Username != "":
			u.User = url.User(c.Username)
		}
		q := url.Values{"sslmode": {"disable"}}
		if c.TLS {
			q.Set("sslmode", "require")
		}
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid PostgreSQL connection: %w", err)
	}
	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = dialTimeout
	}
	cfg.MinConns = 0
	return cfg, nil
}

// mysqlConfig builds the driver config from the connection's URI (a Go DSN
// "user:pass@tcp(host:port)/db?..." or a mysql:// URL) or its fields; TLS
// selects tls=true.
func mysqlConfig(c model.Connection) (*mysql.Config, error) {
	var cfg *mysql.Config
	switch {
	case strings.HasPrefix(strings.ToLower(c.URI), "mysql://"):
		u, err := url.Parse(c.URI)
		if err != nil {
			return nil, errors.New("invalid MySQL URL")
		}
		cfg = mysql.NewConfig()
		cfg.Net = "tcp"
		cfg.Addr = u.Host
		if u.Port() == "" {
			cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
		}
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
		for k, v := range u.Query() {
			switch val := v[len(v)-1]; strings.ToLower(k) {
			case "tls":
				cfg.TLSConfig = val
			case "ssl-mode", "sslmode":
				switch strings.ToUpper(val) {
				case "DISABLED":
					cfg.TLSConfig = "false"
				case "PREFERRED":
					cfg.TLSConfig = "preferred"
				case "REQUIRED":
					cfg.TLSConfig = "skip-verify"
				default:
					cfg.TLSConfig = "true"
				}
			default:
				return nil, fmt.Errorf("unsupported mysql:// parameter %q; use a DSN (user:pass@tcp(host:port)/db?...) for driver options", k)
			}
		}
	case c.URI != "":
		var err error
		if cfg, err = mysql.ParseDSN(c.URI); err != nil {
			return nil, fmt.Errorf("invalid MySQL DSN: %w", err)
		}
	default:
		addr, err := hostPort(&c, 3306)
		if err != nil {
			return nil, err
		}
		cfg = mysql.NewConfig()
		cfg.Net, cfg.Addr = "tcp", addr
		cfg.User, cfg.Passwd, cfg.DBName = c.Username, c.Password, c.Database
		if c.TLS {
			cfg.TLSConfig = "true"
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = dialTimeout
	}
	// Failures are reported through error kinds; the driver's own log
	// would flood the daemon's stderr when a target goes down.
	cfg.Logger = mysql.Logger(log.New(io.Discard, "", 0))
	return cfg, nil
}
