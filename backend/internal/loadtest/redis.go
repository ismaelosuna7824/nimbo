package loadtest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// redisExec sends one command per request. The command is split into
// arguments once; placeholders are rendered per argument, so rendered values
// can never add or split arguments.
type redisExec struct {
	client *redis.Client
	args   []template
}

func newRedisExec(t *model.RedisTarget, c model.Connection, pool int) (*redisExec, error) {
	parts, err := splitCommand(t.Command)
	if err != nil {
		return nil, err
	}
	opts, err := redisOptions(c)
	if err != nil {
		return nil, err
	}
	opts.PoolSize = max(pool, 1)
	r := &redisExec{client: redis.NewClient(opts)}
	for _, p := range parts {
		r.args = append(r.args, compileTemplate(p))
	}
	return r, nil
}

func (r *redisExec) preflight(ctx context.Context) error {
	if _, err := r.send(ctx, 0); err != nil {
		return fmt.Errorf("%s (%s): %w", r.args[0].render(0), classifyRedis(err), err)
	}
	return nil
}

func (r *redisExec) do(ctx context.Context, seq int64) outcome {
	n, err := r.send(ctx, seq)
	o := outcome{bytes: n}
	if err != nil {
		o.errKind = classifyRedis(err)
	}
	return o
}

// send runs the command; a nil reply (missing key) is a success.
func (r *redisExec) send(ctx context.Context, seq int64) (int64, error) {
	args := make([]any, len(r.args))
	for i, a := range r.args {
		args[i] = a.render(seq)
	}
	v, err := r.client.Do(ctx, args...).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return replySize(v), err
}

func (r *redisExec) close() { r.client.Close() }

// replySize approximates the size of a reply's payload.
func replySize(v any) int64 {
	switch x := v.(type) {
	case string:
		return int64(len(x))
	case []byte:
		return int64(len(x))
	case []any:
		var n int64
		for _, e := range x {
			n += replySize(e)
		}
		return n
	case map[any]any:
		var n int64
		for k, e := range x {
			n += replySize(k) + replySize(e)
		}
		return n
	case nil:
		return 0
	default:
		return 8
	}
}

// classifyRedis maps errors to kinds: redis:<prefix> for server errors
// (e.g. redis:WRONGTYPE), auth_failed for rejected credentials.
func classifyRedis(err error) string {
	var re redis.Error
	if errors.As(err, &re) {
		prefix, _, _ := strings.Cut(strings.TrimSpace(re.Error()), " ")
		switch prefix {
		case "NOAUTH", "WRONGPASS":
			return errAuthFailed
		case "":
			return "redis:ERR"
		}
		return "redis:" + prefix
	}
	if errors.Is(err, redis.ErrPoolTimeout) {
		return errClientSaturated
	}
	return classify(err)
}

// splitCommand splits a Redis command on whitespace; double quotes group an
// argument containing spaces, with \" and \\ escapes inside quotes.
func splitCommand(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inArg   bool
		inQuote bool
	)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQuote && ch == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
			i++
			cur.WriteByte(s[i])
		case ch == '"':
			inQuote = !inQuote
			inArg = true
		case !inQuote && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteByte(ch)
			inArg = true
		}
	}
	if inQuote {
		return nil, errors.New("redis command has an unterminated double quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return nil, errors.New("redis target needs a command")
	}
	return args, nil
}

// redisOptions builds client options from the connection's URI (redis:// or
// rediss://) or its fields (Database is the DB number).
func redisOptions(c model.Connection) (*redis.Options, error) {
	var opts *redis.Options
	if c.URI != "" {
		o, err := redis.ParseURL(c.URI)
		if err != nil {
			return nil, fmt.Errorf("invalid Redis URL: %w", err)
		}
		opts = o
	} else {
		addr, err := hostPort(&c, 6379)
		if err != nil {
			return nil, err
		}
		db := 0
		if c.Database != "" {
			if db, err = strconv.Atoi(c.Database); err != nil || db < 0 {
				return nil, fmt.Errorf("redis database must be a number, got %q", c.Database)
			}
		}
		opts = &redis.Options{Addr: addr, Username: c.Username, Password: c.Password, DB: db}
		if c.TLS {
			opts.TLSConfig = &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
		}
	}
	opts.DialTimeout = dialTimeout
	opts.ContextTimeoutEnabled = true
	opts.MaxRetries = -1 // every request is measured as exactly one attempt
	opts.DisableIdentity = true
	return opts, nil
}
