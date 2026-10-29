FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
# go mod tidy writes go.sum on the first build; commit go.sum once it exists.
RUN go mod tidy && go vet ./... && go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
