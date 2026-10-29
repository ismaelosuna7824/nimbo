# Lambda artifact build (Nimbo runs: docker build --target package --output type=local,dest=dist .)
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
# go mod tidy writes go.sum on the first build; commit go.sum once it exists.
RUN go mod tidy && go vet ./... && go test ./...
# Build for the builder's architecture; set the Lambda's architectures to match
# (arm64 on Apple silicon / Graviton, x86_64 otherwise).
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -tags lambda.norpc -trimpath -ldflags="-s -w" -o /out/bootstrap ./cmd/api

FROM scratch AS package
COPY --from=build /out/bootstrap /bootstrap
