package loadtest

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// checkTimeout bounds CheckConnection.
const checkTimeout = 10 * time.Second

// CheckConnection opens the connection and runs one cheap request (HTTP GET
// of the base URL, SELECT version(), PING + INFO, ping + buildInfo,
// sts:GetCallerIdentity). It never fails: problems are reported with OK
// false, a short Message and a Detail with secrets redacted.
func CheckConnection(ctx context.Context, c model.Connection) model.ConnectionCheck {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	t0 := time.Now()
	msg, kind, err := checkConnection(ctx, &c)
	out := model.ConnectionCheck{
		OK:        err == nil,
		LatencyMs: float64(time.Since(t0).Microseconds()) / 1000,
		Message:   redactSecrets(msg, &c),
	}
	if err != nil {
		out.Detail = redactSecrets(err.Error(), &c)
		if out.Message == "" {
			out.Message = checkFailure(kind)
		}
	}
	return out
}

func checkFailure(kind string) string {
	switch kind {
	case errAuthFailed:
		return "Authentication failed"
	case errConnectionRefused:
		return "Connection refused"
	case errTimeout:
		return "Timed out"
	case errDNS:
		return "Host not found"
	case "":
		return "Invalid connection"
	}
	return "Connection failed (" + kind + ")"
}

// checkConnection returns the success message, or the error with its kind.
func checkConnection(ctx context.Context, c *model.Connection) (string, string, error) {
	switch c.Kind {
	case model.ConnHTTP:
		return checkHTTP(ctx, c)
	case model.ConnPostgres:
		cfg, err := postgresConfig(*c)
		if err != nil {
			return "", "", err
		}
		conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
		if err != nil {
			return "", classifySQL(err), err
		}
		defer conn.Close(context.WithoutCancel(ctx))
		var v string
		if err := conn.QueryRow(ctx, "SELECT version()").Scan(&v); err != nil {
			return "", classifySQL(err), err
		}
		return firstWords(v, 2), "", nil
	case model.ConnMySQL:
		cfg, err := mysqlConfig(*c)
		if err != nil {
			return "", "", err
		}
		conn, err := mysql.NewConnector(cfg)
		if err != nil {
			return "", "", err
		}
		db := sql.OpenDB(conn)
		defer db.Close()
		var v string
		if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
			return "", classifySQL(err), err
		}
		if num, _, ok := strings.Cut(v, "-MariaDB"); ok {
			return "MariaDB " + num, "", nil
		}
		return "MySQL " + v, "", nil
	case model.ConnRedis:
		opts, err := redisOptions(*c)
		if err != nil {
			return "", "", err
		}
		opts.PoolSize = 1
		client := redis.NewClient(opts)
		defer client.Close()
		if err := client.Ping(ctx).Err(); err != nil {
			return "", classifyRedis(err), err
		}
		info, err := client.Info(ctx, "server").Result()
		if err != nil {
			return "", classifyRedis(err), err
		}
		for line := range strings.SplitSeq(info, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
				return "Redis " + v, "", nil
			}
		}
		return "Redis", "", nil
	case model.ConnMongo:
		opts, err := mongoOptions(*c)
		if err != nil {
			return "", "", err
		}
		client, err := mongo.Connect(opts.SetMaxPoolSize(1))
		if err != nil {
			return "", classifyMongo(err), err
		}
		defer client.Disconnect(context.WithoutCancel(ctx))
		admin := client.Database("admin")
		if err := admin.RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
			return "", classifyMongo(err), err
		}
		var info struct {
			Version string `bson:"version"`
		}
		if err := admin.RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info); err != nil {
			return "", classifyMongo(err), err
		}
		return "MongoDB " + info.Version, "", nil
	case model.ConnAWS:
		cfg, err := loadAWSConfig(ctx, *c, nil)
		if err != nil {
			return "", "", err
		}
		out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return "", classifyAWS(err), err
		}
		return fmt.Sprintf("AWS account %s · %s", aws.ToString(out.Account), aws.ToString(out.Arn)), "", nil
	}
	return "", "", fmt.Errorf("unknown connection kind %q", c.Kind)
}

// checkHTTP GETs the base URL. The service counts as reachable unless it
// answers 5xx or rejects the credentials (401, 403, 407); a 404 on the base
// URL is common for APIs and still proves connectivity.
func checkHTTP(ctx context.Context, c *model.Connection) (string, string, error) {
	if c.BaseURL == "" {
		return "", "", errors.New("the connection has no base URL")
	}
	tr := newTransport(1)
	if c.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit per-connection opt-in
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header = connHeaders(c)
	resp, err := client.Do(req)
	if err != nil {
		return "", classify(err), err
	}
	resp.Body.Close()
	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if s := resp.Header.Get("Server"); s != "" {
		msg += " · " + s
	}
	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized || code == http.StatusForbidden || code == http.StatusProxyAuthRequired:
		return msg, errAuthFailed, fmt.Errorf("the server rejected the credentials: %s", resp.Status)
	case code >= 500:
		return msg, errHTTP5xx, fmt.Errorf("the server answered %s", resp.Status)
	}
	return msg, "", nil
}

// firstWords returns the first n words of s ("PostgreSQL 16.4 on ..." →
// "PostgreSQL 16.4").
func firstWords(s string, n int) string {
	f := strings.Fields(s)
	return strings.Join(f[:min(n, len(f))], " ")
}
