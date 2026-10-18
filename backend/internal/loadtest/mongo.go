package loadtest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Mongo operations supported by the engine.
const (
	MongoInsertOne  = "insertOne"
	MongoFindOne    = "findOne"
	MongoFind       = "find"
	MongoUpdateOne  = "updateOne"
	MongoDeleteOne  = "deleteOne"
	MongoRunCommand = "runCommand"
)

// mongoExec runs one operation per request. The client connects in
// preflight, so building the executor sends nothing.
type mongoExec struct {
	opts       *options.ClientOptions
	database   string
	collection string
	op         string
	doc, upd   docTemplate

	client *mongo.Client
	db     *mongo.Database
	coll   *mongo.Collection
}

func newMongoExec(t *model.MongoTarget, c model.Connection, pool int) (*mongoExec, error) {
	m := &mongoExec{database: orDefault(t.Database, c.Database), collection: t.Collection, op: t.Operation}
	if m.database == "" {
		return nil, errors.New("mongo target needs a database")
	}
	var err error
	switch t.Operation {
	case MongoInsertOne:
		if strings.TrimSpace(t.Document) == "" {
			return nil, errors.New("insertOne needs a document")
		}
		m.doc, err = newDocTemplate(t.Document, "document")
	case MongoFindOne, MongoFind, MongoDeleteOne:
		m.doc, err = newDocTemplate(orDefault(t.Document, "{}"), "filter")
	case MongoUpdateOne:
		if m.doc, err = newDocTemplate(orDefault(t.Document, "{}"), "filter"); err == nil {
			if strings.TrimSpace(t.Update) == "" {
				return nil, errors.New("updateOne needs an update document")
			}
			m.upd, err = newDocTemplate(t.Update, "update")
		}
	case MongoRunCommand:
		if strings.TrimSpace(t.Document) == "" {
			return nil, errors.New("runCommand needs a command document")
		}
		m.doc, err = newDocTemplate(t.Document, "command")
	default:
		return nil, fmt.Errorf("unknown mongo operation %q (insertOne, findOne, find, updateOne, deleteOne or runCommand)", t.Operation)
	}
	if err != nil {
		return nil, err
	}
	if t.Operation != MongoRunCommand && t.Collection == "" {
		return nil, fmt.Errorf("%s needs a collection", t.Operation)
	}
	if m.opts, err = mongoOptions(c); err != nil {
		return nil, err
	}
	m.opts.SetMaxPoolSize(uint64(max(pool, 1)))
	return m, nil
}

func (m *mongoExec) preflight(ctx context.Context) error {
	client, err := mongo.Connect(m.opts)
	if err != nil {
		return err
	}
	m.client = client
	m.db = client.Database(m.database)
	if m.collection != "" {
		m.coll = m.db.Collection(m.collection)
	}
	if _, err := m.run(ctx, 0); err != nil {
		return fmt.Errorf("%s (%s): %w", m.op, classifyMongo(err), err)
	}
	return nil
}

func (m *mongoExec) do(ctx context.Context, seq int64) outcome {
	n, err := m.run(ctx, seq)
	o := outcome{bytes: n}
	if err != nil {
		o.errKind = classifyMongo(err)
	}
	return o
}

// run executes the operation and returns the size of the documents read. A
// findOne without a match is a success.
func (m *mongoExec) run(ctx context.Context, seq int64) (int64, error) {
	doc, err := m.doc.render(seq)
	if err != nil {
		return 0, &opError{kind: errInvalidPayload, msg: err.Error()}
	}
	switch m.op {
	case MongoInsertOne:
		_, err = m.coll.InsertOne(ctx, doc)
		return 0, err
	case MongoFindOne:
		raw, err := m.coll.FindOne(ctx, doc).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, nil
		}
		return int64(len(raw)), err
	case MongoFind:
		cur, err := m.coll.Find(ctx, doc)
		if err != nil {
			return 0, err
		}
		defer cur.Close(context.WithoutCancel(ctx))
		var n int64
		for cur.Next(ctx) {
			n += int64(len(cur.Current))
		}
		return n, cur.Err()
	case MongoUpdateOne:
		upd, err := m.upd.render(seq)
		if err != nil {
			return 0, &opError{kind: errInvalidPayload, msg: err.Error()}
		}
		_, err = m.coll.UpdateOne(ctx, doc, upd)
		return 0, err
	case MongoDeleteOne:
		_, err = m.coll.DeleteOne(ctx, doc)
		return 0, err
	default: // runCommand
		raw, err := m.db.RunCommand(ctx, doc).Raw()
		return int64(len(raw)), err
	}
}

func (m *mongoExec) close() {
	if m.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		_ = m.client.Disconnect(ctx)
	}
}

// docTemplate is a JSON (MongoDB Extended JSON, relaxed or canonical)
// document with placeholders; static documents are parsed once.
type docTemplate struct {
	t     template
	fixed bson.D
}

func newDocTemplate(s, what string) (docTemplate, error) {
	d := docTemplate{t: compileTemplate(s)}
	doc, err := parseDoc(d.t.render(1))
	if err != nil {
		return d, fmt.Errorf("%s must be a JSON object: %w", what, err)
	}
	if d.t.static() {
		d.fixed = doc
	}
	return d, nil
}

func (d docTemplate) render(seq int64) (bson.D, error) {
	if d.fixed != nil {
		return d.fixed, nil
	}
	return parseDoc(d.t.render(seq))
}

func parseDoc(s string) (bson.D, error) {
	var doc bson.D
	if err := bson.UnmarshalExtJSON([]byte(s), false, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = bson.D{}
	}
	return doc, nil
}

// classifyMongo maps errors to kinds: mongo:<code name> for server errors,
// auth_failed for rejected credentials.
func classifyMongo(err error) string {
	var oe *opError
	if errors.As(err, &oe) {
		return oe.kind
	}
	if mongo.IsDuplicateKeyError(err) {
		return "mongo:DuplicateKey"
	}
	var ce mongo.CommandError
	if errors.As(err, &ce) {
		switch {
		case ce.Code == 18 || ce.Name == "AuthenticationFailed":
			return errAuthFailed
		case ce.Name != "":
			return "mongo:" + ce.Name
		case ce.Code != 0:
			return "mongo:" + strconv.Itoa(int(ce.Code))
		}
	}
	var we mongo.WriteException
	if errors.As(err, &we) {
		switch {
		case len(we.WriteErrors) > 0:
			return "mongo:" + strconv.Itoa(we.WriteErrors[0].Code)
		case we.WriteConcernError != nil && we.WriteConcernError.Name != "":
			return "mongo:" + we.WriteConcernError.Name
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "AuthenticationFailed"), strings.Contains(msg, "auth error"):
		return errAuthFailed
	case mongo.IsTimeout(err) && !strings.Contains(msg, "connection refused"):
		return errTimeout
	case strings.Contains(msg, "connection refused"):
		return errConnectionRefused
	}
	return classify(err)
}

// mongoOptions builds client options from the connection's URI or fields.
// Retries are off so every request is measured as one attempt.
func mongoOptions(c model.Connection) (*options.ClientOptions, error) {
	o := options.Client()
	if c.URI != "" {
		o.ApplyURI(c.URI)
	} else {
		addr, err := hostPort(&c, 27017)
		if err != nil {
			return nil, err
		}
		o.SetHosts([]string{addr})
		if c.Username != "" {
			o.SetAuth(options.Credential{Username: c.Username, Password: c.Password})
		}
		if c.TLS {
			o.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
		}
	}
	o.SetConnectTimeout(dialTimeout).
		SetServerSelectionTimeout(dialTimeout).
		SetRetryWrites(false).
		SetRetryReads(false).
		SetAppName("nimbo-loadtest")
	if err := o.Validate(); err != nil {
		return nil, fmt.Errorf("invalid MongoDB connection: %w", err)
	}
	return o, nil
}
