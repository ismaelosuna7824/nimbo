// Composition root + Lambda driving adapter (API Gateway HTTP API, payload 2.0).
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"

	"example.com/api/internal/adapters/inbound/httpapi"
	"example.com/api/internal/adapters/outbound/store"
	"example.com/api/internal/application"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	repo, err := store.New(context.Background(), os.Getenv)
	if err != nil {
		log.Error("startup", "error", err)
		os.Exit(1)
	}
	routes := httpapi.Routes{Items: application.Items{Repo: repo, NewID: rand.Text, Now: time.Now}, Log: log}

	lambda.Start(func(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		reqLog := log
		if lc, ok := lambdacontext.FromContext(ctx); ok {
			reqLog = log.With("requestId", lc.AwsRequestID)
		}
		res := httpapi.Routes{Items: routes.Items, Log: reqLog}.Handle(ctx, ev.RequestContext.HTTP.Method, ev.RawPath, []byte(ev.Body))
		body, _ := json.Marshal(res.Body)
		reqLog.Info("request", "path", ev.RawPath, "status", res.Status)
		return events.APIGatewayV2HTTPResponse{StatusCode: res.Status, Headers: map[string]string{"content-type": "application/json"}, Body: string(body)}, nil
	})
}
