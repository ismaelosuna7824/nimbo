package compose

import "testing"

func TestImageBases(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  api:
    build: ./api
  worker:
    build:
      context: ./api
      target: deps
  go:
    build:
      context: ./go
      args: {GO_VERSION: "1.23"}
  inline:
    build:
      context: .
      dockerfile_inline: "FROM alpine\nRUN true\n"
  db:
    image: postgres:16
`,
		"api/Dockerfile": `FROM node:22 AS deps
WORKDIR /app
COPY package*.json ./
RUN npm ci
FROM deps AS build
COPY . .
RUN npm run build
FROM node:22-alpine
COPY --from=build /app/dist /app
USER node
CMD ["node", "/app/main.js"]
`,
		"go/Dockerfile": `ARG GO_VERSION=1.22
FROM golang:${GO_VERSION} AS build
RUN go build -o /out/app .
FROM build AS test
RUN go test ./...
FROM scratch
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
`,
	})
	got := p.ImageBases()
	want := map[string]ImageBase{
		"api":    {Image: "node:22-alpine", OwnLayers: 3},
		"worker": {Image: "node:22", OwnLayers: 3},
		"go":     {Image: "scratch", OwnLayers: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("ImageBases = %+v", got)
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: %+v, want %+v", name, got[name], w)
		}
	}
	// A stage built FROM another stage carries that stage's layers.
	p = open(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    build: .\n",
		"Dockerfile":   "FROM python:3.12-slim AS base\nENV A=1\nRUN pip install x\nFROM base\nCOPY . /app\nCMD [\"python\", \"/app/main.py\"]\n",
	})
	if got := p.ImageBases()["app"]; got != (ImageBase{Image: "python:3.12-slim", OwnLayers: 4}) {
		t.Errorf("chained stages: %+v", got)
	}
}
