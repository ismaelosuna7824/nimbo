// Package httpapi is the driving adapter: HTTP -> use cases, domain errors -> status codes.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"example.com/api/internal/application"
	"example.com/api/internal/domain"
)

const maxBody = 16 << 10 // OWASP API4: bound resource consumption

var itemPath = regexp.MustCompile(`/items/([\w-]{1,64})$`)

type Response struct {
	Status int
	Body   any
}

type Routes struct {
	Items application.Items
	Log   *slog.Logger
}

func (r Routes) Handle(ctx context.Context, method, path string, body []byte) Response {
	switch {
	case method == "GET" && strings.HasSuffix(path, "/health"):
		return Response{200, map[string]string{"status": "ok"}}
	case method == "POST" && strings.HasSuffix(path, "/items"):
		if len(body) > maxBody {
			return Response{413, errBody("body too large")}
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return Response{400, errBody("body must be JSON")}
		}
		v, err := r.Items.Create(ctx, in.Name)
		return r.result(ctx, 201, v, err)
	case method == "GET" && itemPath.MatchString(path):
		v, err := r.Items.Get(ctx, itemPath.FindStringSubmatch(path)[1])
		return r.result(ctx, 200, v, err)
	}
	return Response{404, errBody("route not found")}
}

func (r Routes) result(ctx context.Context, ok int, v any, err error) Response {
	switch {
	case err == nil:
		return Response{ok, v}
	case errors.Is(err, domain.ErrInvalid):
		return Response{400, errBody(err.Error())}
	case errors.Is(err, domain.ErrNotFound):
		return Response{404, errBody("item not found")}
	case errors.Is(err, domain.ErrConflict):
		return Response{409, errBody("item already exists")}
	}
	r.Log.ErrorContext(ctx, "unhandled", "error", err)
	return Response{500, errBody("internal error")} // never leak internals
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }
