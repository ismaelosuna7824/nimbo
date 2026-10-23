import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import {
  TECH_ICON_IDS,
  imageIconId,
  isTechIconId,
  parseImageRef,
  stackIconId,
  techIcon,
  techIconFor,
  techIconSrc,
  type TechIconId,
} from "./index";

const ASSETS = join(import.meta.dir, "assets");

describe("tech icon registry", () => {
  test("has the ids of the Docker workspace contract", () => {
    const required =
      "docker kubernetes nginx apache caddy traefik haproxy envoy go node deno bun python rust java kotlin dotnet csharp php ruby elixir scala swift cpp typescript postgres mysql mariadb mongodb redis valkey memcached sqlserver oracle-db cassandra elasticsearch opensearch clickhouse rabbitmq kafka nats minio localstack keycloak vault consul grafana prometheus jaeger otel loki wordpress ghost nextjs spring django fastapi flask rails laravel express nestjs alpine ubuntu debian distroless busybox container volume network secret config build-stage external";
    for (const id of required.split(" ")) expect(isTechIconId(id)).toBe(true);
  });

  test("every id has exactly one optimised asset", () => {
    const files = readdirSync(ASSETS).sort();
    expect(files).toEqual(TECH_ICON_IDS.map((id) => `${id}.svg`).sort());
    for (const file of files) {
      const svg = readFileSync(join(ASSETS, file), "utf8");
      expect(svg.startsWith("<svg")).toBe(true);
      expect(svg).toContain("viewBox=");
      expect(svg).not.toMatch(/<script|<foreignObject|xlink:href="http|href="http|on[a-z]+="/i);
      // Ids are prefixed per file so inlined copies never collide.
      for (const m of svg.matchAll(/\bid="([^"]+)"/g)) expect(m[1].startsWith(`ti-${file.slice(0, -4)}`)).toBe(true);
      expect(statSync(join(ASSETS, file)).size).toBeLessThan(20_000);
    }
  });

  test("techIcon resolves ids, unknown ids fall back to the container", () => {
    expect(techIcon("postgres")).toMatchObject({ id: "postgres", label: "PostgreSQL", category: "database", generic: false });
    expect(techIcon("postgres").src).toBe(techIconSrc("postgres"));
    expect(techIcon("postgres").src).toMatch(/assets\/postgres\.svg$/);
    expect(techIcon("nope")).toMatchObject({ id: "container", generic: true });
    expect(techIcon("constructor").id).toBe("container");
    expect(techIcon("rust").darkInk).toBe(true);
    expect(techIcon("node").darkInk).toBe(false);
  });
});

describe("parseImageRef", () => {
  test.each([
    ["postgres", { registry: "docker.io", repository: "library/postgres", name: "postgres" }],
    ["postgres:16-alpine", { registry: "docker.io", repository: "library/postgres", name: "postgres", tag: "16-alpine" }],
    ["docker.io/library/nginx:1.27", { registry: "docker.io", repository: "library/nginx", name: "nginx", tag: "1.27" }],
    ["index.docker.io/library/redis", { registry: "docker.io", repository: "library/redis", name: "redis" }],
    ["bitnami/redis:7.2", { registry: "docker.io", repository: "bitnami/redis", name: "redis", tag: "7.2" }],
    ["ghcr.io/org/app:sha-1a2b3c", { registry: "ghcr.io", repository: "org/app", name: "app", tag: "sha-1a2b3c" }],
    ["localhost:5000/x", { registry: "localhost:5000", repository: "x", name: "x" }],
    ["localhost/x:dev", { registry: "localhost", repository: "x", name: "x", tag: "dev" }],
    [
      "postgres@sha256:0123abcd",
      { registry: "docker.io", repository: "library/postgres", name: "postgres", digest: "sha256:0123abcd" },
    ],
    [
      "mcr.microsoft.com/dotnet/aspnet:8.0@sha256:ff",
      { registry: "mcr.microsoft.com", repository: "dotnet/aspnet", name: "aspnet", tag: "8.0", digest: "sha256:ff" },
    ],
    ["registry.example.com:8443/team/svc/api:1", { registry: "registry.example.com:8443", repository: "team/svc/api", name: "api", tag: "1" }],
    ["${REGISTRY:-ghcr.io}/org/api:${TAG:-latest}", { registry: "docker.io", repository: "org/api", name: "api" }],
    ["Postgres:16", { registry: "docker.io", repository: "library/postgres", name: "postgres", tag: "16" }],
  ] as const)("%s", (ref, want) => {
    expect(parseImageRef(ref)).toEqual({ tag: undefined, digest: undefined, ...want });
  });

  test("references without a repository", () => {
    expect(parseImageRef("")).toBeUndefined();
    expect(parseImageRef("${IMAGE}")).toBeUndefined();
    expect(parseImageRef("$IMAGE")).toBeUndefined();
  });
});

describe("imageIconId", () => {
  const cases: [string, TechIconId | undefined][] = [
    // Official images, tags, digests, registry hosts
    ["nginx", "nginx"],
    ["nginx:1.27-alpine", "nginx"],
    ["docker.io/library/nginx:stable", "nginx"],
    ["public.ecr.aws/docker/library/nginx:latest", "nginx"],
    ["mirror.gcr.io/library/redis:7", "redis"],
    ["nginxinc/nginx-unprivileged:1.27", "nginx"],
    ["openresty/openresty:alpine", "nginx"],
    ["httpd:2.4", "apache"],
    ["caddy:2-alpine", "caddy"],
    ["traefik:v3.1", "traefik"],
    ["haproxy:2.9", "haproxy"],
    ["envoyproxy/envoy:v1.31-latest", "envoy"],
    ["tomcat:10-jdk21", "tomcat"],
    ["postgres", "postgres"],
    ["postgres:16.4-bookworm", "postgres"],
    ["postgres@sha256:4ec37d2a07a0067f176fdcc9d4bb633a5724d2cc4f892c7a2046d054bb6939e5", "postgres"],
    ["bitnami/postgresql:16", "postgres"],
    ["postgis/postgis:16-3.4", "postgres"],
    ["timescale/timescaledb:2.31.0-pg17", "timescale"],
    ["postgrest/postgrest:v14.18", "postgres"],
    ["pgvector/pgvector:pg16", "postgres"],
    ["dpage/pgadmin4", "postgres"],
    ["mysql:8.4", "mysql"],
    ["percona/percona-server:8.0", "mysql"],
    ["container-registry.oracle.com/mysql/community-server:8.4", "mysql"],
    ["mariadb:11", "mariadb"],
    ["mongo:7", "mongodb"],
    ["mongo-express", "mongodb"],
    ["mongodb/mongodb-community-server:7.0-ubi8", "mongodb"],
    ["mcr.microsoft.com/mssql/server:2022-latest", "sqlserver"],
    ["mcr.microsoft.com/azure-sql-edge", "sqlserver"],
    ["gvenzl/oracle-free:23-slim", "oracle-db"],
    ["container-registry.oracle.com/database/express:21.3.0-xe", "oracle-db"],
    ["cassandra:5", "cassandra"],
    ["clickhouse/clickhouse-server:24.8", "clickhouse"],
    ["neo4j:5-community", "neo4j"],
    ["couchdb:3", "couchdb"],
    ["influxdb:2.7", "influxdb"],
    ["cockroachdb/cockroach:v24.1.0", "cockroachdb"],
    ["amazon/dynamodb-local", "dynamodb"],
    ["supabase/postgres:15.1.0.147", "supabase"],
    ["adminer", "adminer"],
    ["redis:7-alpine", "redis"],
    ["bitnami/redis", "redis"],
    ["redis/redis-stack-server:latest", "redis"],
    ["valkey/valkey:8", "valkey"],
    ["memcached:1.6", "memcached"],
    ["docker.elastic.co/elasticsearch/elasticsearch:8.15.0", "elasticsearch"],
    ["docker.elastic.co/kibana/kibana:8.15.0", "kibana"],
    ["kibana:8.15.0", "kibana"],
    ["mcr.microsoft.com/azure-storage/azurite", "azure"],
    ["mcr.microsoft.com/cosmosdb/linux/azure-cosmos-emulator", "azure"],
    ["axllent/mailpit:v1.20", "mail"],
    ["mailhog/mailhog", "mail"],
    ["gitea/gitea:1.22-rootless", "gitea"],
    ["opensearchproject/opensearch:2", "opensearch"],
    ["opensearchproject/opensearch-dashboards:2", "opensearch"],
    ["rabbitmq:3-management", "rabbitmq"],
    ["apache/kafka:3.8.0", "kafka"],
    ["bitnami/kafka:3.7", "kafka"],
    ["confluentinc/cp-kafka:7.6.1", "kafka"],
    ["confluentinc/cp-zookeeper:7.6.1", "kafka"],
    ["nats:2.10", "nats"],
    ["eclipse-mosquitto:2", "mosquitto"],
    ["minio/minio:RELEASE.2024-08-17T01-24-54Z", "minio"],
    ["pgsty/minio:RELEASE.2026-08-04T00-00-00Z", "minio"],
    ["quay.io/minio/minio", "minio"],
    ["localstack/localstack:3", "localstack"],
    ["quay.io/keycloak/keycloak:25.0", "keycloak"],
    ["hashicorp/vault:1.17", "vault"],
    ["hashicorp/consul:1.19", "consul"],
    ["grafana/grafana-oss:11.1.0", "grafana"],
    ["grafana/tempo:latest", "grafana"],
    ["grafana/loki:3.1.0", "loki"],
    ["grafana/promtail:3.1.0", "loki"],
    ["prom/prometheus:v2.53.0", "prometheus"],
    ["prom/node-exporter", "prometheus"],
    ["bitnami/node-exporter", "prometheus"],
    ["quay.io/prometheus/alertmanager", "prometheus"],
    ["jaegertracing/all-in-one:1.60", "jaeger"],
    ["otel/opentelemetry-collector-contrib:0.107.0", "otel"],
    ["getsentry/sentry:24.8.0", "sentry"],
    ["redislabs/redismod", "redis"],
    ["redis/redis-stack-server:7.4.0-v0", "redis"],
    ["wordpress:6-php8.3-apache", "wordpress"],
    ["wordpress:cli", "wordpress"],
    ["ghost:5-alpine", "ghost"],
    ["drupal:10", "drupal"],
    ["nextcloud:29-apache", "nextcloud"],
    ["strapi/strapi", "strapi"],
    ["directus/directus:11", "directus"],
    ["n8nio/n8n", "n8n"],
    ["docker.n8n.io/n8nio/n8n", "n8n"],
    ["nodered/node-red:4.0", "nodered"],
    ["apache/airflow:2.10.0", "airflow"],
    ["quay.io/jupyter/scipy-notebook", "jupyter"],
    ["jupyter/base-notebook", "jupyter"],
    ["jenkins/jenkins:lts-jdk17", "jenkins"],
    ["gitlab/gitlab-ce:17.3.0-ce.0", "gitlab"],
    ["gitlab/gitlab-runner:alpine", "gitlab"],
    ["sonarqube:10-community", "sonarqube"],
    ["portainer/portainer-ce:2.21", "portainer"],
    ["portainer/agent", "portainer"],
    ["docker:27-dind", "docker"],
    ["registry:2", "docker"],
    ["moby/buildkit:latest", "docker"],
    ["bitnami/kubectl:1.31", "kubernetes"],
    ["rancher/k3s:v1.30.4-k3s1", "kubernetes"],
    ["kindest/node:v1.31.0", "kubernetes"],
    ["alpine/helm:3.15", "helm"],
    // Catalog images whose repository or name differs from the product
    ["nginxinc/nginx-unprivileged:1.30-alpine", "nginx"],
    ["varnish:7.7", "varnish"],
    ["ubuntu/squid:6.6-24.04_edge", "squid"],
    ["kong:3.9.4", "kong"],
    ["apache/apisix:3.19.0-debian", "apisix"],
    ["devopsfaith/krakend:2.9.4", "krakend"],
    ["quay.io/oauth2-proxy/oauth2-proxy:v7.14.2-alpine", "oauth"],
    ["jetty:12.1-jdk21-eclipse-temurin", "jetty"],
    ["quay.io/wildfly/wildfly:39.0.0.Final-jdk21", "wildfly"],
    ["cockroachdb/cockroach:v25.4.18", "cockroachdb"],
    ["questdb/questdb:9.3.5", "questdb"],
    ["surrealdb/surrealdb:v3.3.4", "surrealdb"],
    ["scylladb/scylla:2026.1", "scylladb"],
    ["arangodb:3.12.12", "arangodb"],
    ["couchbase/server:community-8.0.0", "couchbase"],
    ["gcr.io/etcd-development/etcd:v3.6.15", "etcd"],
    ["docker.dragonflydb.io/dragonflydb/dragonfly:v1.40.2", "dragonfly"],
    ["getmeili/meilisearch:v1.55.0", "meilisearch"],
    ["typesense/typesense:30.2", "typesense"],
    ["solr:9.11", "solr"],
    ["qdrant/qdrant:v1.20.0", "qdrant"],
    ["cr.weaviate.io/semitechnologies/weaviate:1.40.2", "weaviate"],
    ["chromadb/chroma:1.5.9", "chroma"],
    ["milvusdb/milvus:v2.6.25", "milvus"],
    ["apache/activemq-artemis:2.44.0-alpine", "activemq"],
    ["apache/activemq-classic:6.2.0", "activemq"],
    ["apachepulsar/pulsar:4.0.14", "pulsar"],
    ["nsqio/nsq:v1.3.0", "nsq"],
    ["zookeeper:3.9", "zookeeper"],
    ["kafbat/kafka-ui:v1.5.0", "kafka"],
    ["emqx/emqx:5.8.9", "emqx"],
    ["rustfs/rustfs:1.0.3", "rustfs"],
    ["ghcr.io/goauthentik/server:2026.8.5", "authentik"],
    ["oryd/kratos:v26.2.0", "ory"],
    ["ghcr.io/zitadel/zitadel:v4.20.0", "zitadel"],
    ["ghcr.io/dexidp/dex:v2.46.1-alpine", "dex"],
    ["svhd/logto:1.45.0", "logto"],
    ["vaultwarden/server:1.37.4-alpine", "vaultwarden"],
    ["prom/pushgateway:v1.11.5", "prometheus"],
    ["grafana/alloy:v1.21.1", "grafana"],
    ["victoriametrics/victoria-metrics:v1.154.1", "victoriametrics"],
    ["openzipkin/zipkin:3.6.1", "zipkin"],
    ["openobserve/openobserve:v1.1.2", "openobserve"],
    ["netdata/netdata:v2.12.1", "netdata"],
    ["ghcr.io/google/cadvisor:0.60.6", "cadvisor"],
    ["louislam/uptime-kuma:2.5.6", "uptimekuma"],
    ["temporalio/temporal:1.9.1", "temporal"],
    ["temporalio/ui:2.57.0", "temporal"],
    ["prefecthq/prefect:3.9.0-python3.12", "prefect"],
    ["metabase/metabase:v0.56.25", "metabase"],
    ["apache/superset:6.0.0", "superset"],
    ["apache/nifi:2.12.0", "nifi"],
    ["ollama/ollama:0.40.8", "ollama"],
    ["ghcr.io/open-webui/open-webui:v0.12.3", "openwebui"],
    ["ghcr.io/berriai/litellm:v1.106.2", "litellm"],
    ["flowiseai/flowise:3.1.4", "flowise"],
    ["woodpeckerci/woodpecker-server:v3.19.0-alpine", "woodpecker"],
    ["verdaccio/verdaccio:6.10.7", "verdaccio"],
    ["sonatype/nexus3:3.97.1", "sonatype"],
    ["ghcr.io/project-zot/zot:v2.1.22", "zot"],
    ["codercom/code-server:4.141.0", "vscode"],
    ["wiremock/wiremock:3.13.2", "wiremock"],
    ["swaggerapi/swagger-ui:v5.34.0", "swagger"],
    ["selenium/standalone-chromium:4.51.0", "selenium"],
    ["mattermost/mattermost-team-edition:11.11.3", "mattermost"],
    ["outlinewiki/outline:1.11.0", "outline"],
    ["ghcr.io/plausible/community-edition:v3.2.1", "plausible"],
    ["ghcr.io/umami-software/umami:3.4.0", "umami"],
    ["matomo:5.14.0-apache", "matomo"],
    ["appsmith/appsmith-ce:v2.5.1", "appsmith"],
    ["hasura/graphql-engine:v2.48.16", "hasura"],
    ["ghcr.io/muchobien/pocketbase:0.40.5", "pocketbase"],
    ["nocodb/nocodb:2026.09.1", "nocodb"],
    ["coredns/coredns:1.15.0", "coredns"],
    ["pihole/pihole:2026.10.0", "pihole"],
    // Language runtimes / build bases
    ["golang:1.23-alpine", "go"],
    ["node:20-alpine", "node"],
    ["node:lts-bookworm-slim", "node"],
    ["cgr.dev/chainguard/node:latest", "node"],
    ["cgr.dev/chainguard/python:latest-dev", "python"],
    ["cgr.dev/chainguard/static", "chainguard"],
    ["cgr.dev/chainguard/wolfi-base", "chainguard"],
    ["denoland/deno:2.0.0", "deno"],
    ["oven/bun:1", "bun"],
    ["python:3.12-slim", "python"],
    ["pypy:3.10", "python"],
    ["public.ecr.aws/lambda/python:3.12", "python"],
    ["mcr.microsoft.com/devcontainers/python:3.12", "python"],
    ["rust:1.80-slim-bookworm", "rust"],
    ["eclipse-temurin:21-jre-alpine", "java"],
    ["openjdk:17-jdk-slim", "java"],
    ["amazoncorretto:21", "java"],
    ["ibm-semeru-runtimes:open-21-jre", "java"],
    ["bellsoft/liberica-openjdk-alpine:21", "java"],
    ["container-registry.oracle.com/java/openjdk:21", "java"],
    ["maven:3.9-eclipse-temurin-21", "maven"],
    ["gradle:8-jdk21", "gradle"],
    ["zenika/kotlin", "kotlin"],
    ["sbtscala/scala-sbt:eclipse-temurin-17.0.4_1.7.1_3.2.0", "scala"],
    ["mcr.microsoft.com/dotnet/aspnet:8.0", "dotnet"],
    ["mcr.microsoft.com/dotnet/sdk:8.0-alpine", "dotnet"],
    ["mcr.microsoft.com/dotnet/runtime:8.0", "dotnet"],
    ["mcr.microsoft.com/dotnet/runtime-deps:8.0-jammy-chiseled", "dotnet"],
    ["mono:6.12", "csharp"],
    ["php:8.3-fpm-alpine", "php"],
    ["php:8.2-apache", "php"],
    ["bitnami/php-fpm:8.3", "php"],
    ["ruby:3.3-slim", "ruby"],
    ["elixir:1.17-alpine", "elixir"],
    ["erlang:27", "erlang"],
    ["swift:5.10-jammy", "swift"],
    ["gcc:14", "cpp"],
    ["silkeh/clang:18", "cpp"],
    ["haskell:9.8", "haskell"],
    ["dart:stable", "dart"],
    ["perl:5.40", "perl"],
    ["julia:1.10", "julia"],
    ["r-base:4.4.1", "r"],
    ["rocker/r-ver:4.4", "r"],
    // OS / minimal bases
    ["alpine:3.20", "alpine"],
    ["ubuntu:24.04", "ubuntu"],
    ["debian:bookworm-slim", "debian"],
    ["busybox:1.36-musl", "busybox"],
    ["gcr.io/distroless/static-debian12:nonroot", "distroless"],
    ["gcr.io/distroless/base-debian12", "distroless"],
    ["gcr.io/distroless/nodejs20-debian12", "node"],
    ["gcr.io/distroless/java21-debian12", "java"],
    ["gcr.io/distroless/python3-debian12", "python"],
    ["registry.access.redhat.com/ubi9/ubi-minimal:9.4", "redhat"],
    ["registry.access.redhat.com/ubi9/nodejs-20", "node"],
    ["registry.access.redhat.com/ubi8/openjdk-17:1.20", "java"],
    ["centos:7", "centos"],
    ["fedora:40", "fedora"],
    ["rockylinux:9", "rockylinux"],
    ["amazonlinux:2023", "linux"],
    // Own images and unresolvable references
    ["ghcr.io/org/app:sha-4f2a1c9", undefined],
    ["localhost:5000/x", undefined],
    ["myorg/backend:1.4.2", undefined],
    ["123456789012.dkr.ecr.eu-west-1.amazonaws.com/web:prod", undefined],
    ["scratch", undefined],
    ["constructor", undefined],
    ["${IMAGE}", undefined],
  ];
  test(`${cases.length} real image references`, () => {
    expect(cases.length).toBeGreaterThanOrEqual(80);
    const wrong = cases.map(([image, want]) => [image, imageIconId(image), want]).filter(([, got, want]) => got !== want);
    expect(wrong).toEqual([]);
  });
});

describe("stackIconId", () => {
  test.each([
    [["express", "pg"], "express"],
    [["@nestjs/core", "@nestjs/common", "express"], "nestjs"],
    [["next", "react", "react-dom"], "nextjs"],
    [["react"], "react"],
    [["vue", "nuxt"], "nuxt"],
    [["@angular/core"], "angular"],
    [["@sveltejs/kit"], "svelte"],
    [["org.springframework.boot:spring-boot-starter-web"], "spring"],
    [["spring-boot-starter-data-jpa"], "spring"],
    [["io.quarkus:quarkus-rest"], "quarkus"],
    [["fastapi==0.110.0", "uvicorn[standard]"], "fastapi"],
    [["Django>=5.0"], "django"],
    [["flask"], "flask"],
    [["rails"], "rails"],
    [["laravel/framework"], "laravel"],
    [["symfony/framework-bundle"], "symfony"],
    [["phoenix"], "phoenix"],
    [["github.com/gin-gonic/gin"], "gin"],
  ] as const)("%p", (stack, want) => {
    expect(stackIconId(stack)).toBe(want);
  });

  test("no framework", () => {
    expect(stackIconId(undefined)).toBeUndefined();
    expect(stackIconId([])).toBeUndefined();
    expect(stackIconId(["lodash", "pg", "requests", "toString"])).toBeUndefined();
  });

  test("hints of the Docker catalog detector", () => {
    const frameworks: [string[], TechIconId | undefined][] = [
      [["node", "typescript", "fastify"], "fastify"],
      [["node", "koa"], "koa"],
      [["node", "hono"], "hono"],
      [["node", "remix", "react"], "remix"],
      [["node", "astro"], "astro"],
      [["node", "express", "socketio", "prisma"], "express"],
      [["node", "socketio"], "socketio"],
      [["node", "prisma"], "prisma"],
      [["python", "fastapi", "uvicorn", "celery"], "fastapi"],
      [["python", "django", "gunicorn", "celery"], "django"],
      [["python", "celery"], "celery"],
      [["python", "streamlit"], "streamlit"],
      [["python", "aiohttp"], "aiohttp"],
      [["go", "fiber"], "fiber"],
      [["rust", "actix", "tokio"], "actix"],
      [["rust", "rocket"], "rocket"],
      [["rust", "tokio"], "tokio"],
      // No icon of their own: the language decides.
      [["go", "echo"], undefined],
      [["go", "gorilla"], undefined],
      [["java", "micronaut"], undefined],
      [["dotnet", "aspnet"], undefined],
      [["ruby", "sinatra"], undefined],
      [["php", "slim"], undefined],
      [["rust", "axum"], undefined],
    ];
    expect(frameworks.filter(([stack, want]) => stackIconId(stack) !== want)).toEqual([]);
  });
});

describe("techIconFor", () => {
  const id = (q: Parameters<typeof techIconFor>[0]) => techIconFor(q).id;

  test("image-only services use their image", () => {
    expect(id({ kind: "service", image: "postgres:16" })).toBe("postgres");
    expect(id({ kind: "service", image: "node:20" })).toBe("node");
    expect(id({ image: "redis" })).toBe("redis");
  });

  test("built services are their runtime stage, not the image name", () => {
    expect(id({ kind: "service", image: "myorg/redis-worker", baseImages: ["python:3.12-slim"] })).toBe("python");
    expect(id({ kind: "service", baseImages: ["node:20", "nginx:alpine"] })).toBe("nginx");
    expect(id({ kind: "service", baseImages: ["golang:1.23", "gcr.io/distroless/static-debian12"] })).toBe("go");
    expect(id({ kind: "service", baseImages: ["maven:3.9", "eclipse-temurin:21-jre"] })).toBe("java");
    expect(id({ kind: "service", baseImages: ["mcr.microsoft.com/dotnet/sdk:8.0", "mcr.microsoft.com/dotnet/aspnet:8.0"] })).toBe("dotnet");
    expect(id({ kind: "service", baseImages: ["rust:1.80", "debian:bookworm-slim"] })).toBe("rust");
    expect(id({ kind: "service", baseImages: ["postgres:16"], image: "myorg/db" })).toBe("postgres");
    expect(id({ kind: "service", baseImages: ["alpine:3.20"] })).toBe("alpine");
  });

  test("frameworks from stack hints win over the language", () => {
    expect(id({ baseImages: ["node:20-alpine"], stack: ["@nestjs/core", "express"] })).toBe("nestjs");
    expect(id({ baseImages: ["maven:3.9", "eclipse-temurin:21-jre"], stack: ["org.springframework.boot:spring-boot-starter-web"] })).toBe("spring");
    expect(id({ baseImages: ["python:3.12"], stack: ["fastapi"] })).toBe("fastapi");
    expect(id({ baseImages: ["golang:1.23", "gcr.io/distroless/static"], stack: ["github.com/gin-gonic/gin"] })).toBe("gin");
    expect(id({ image: "node:20", stack: ["next"] })).toBe("nextjs");
    // A product image is what runs, whatever the context's dependencies say.
    expect(id({ baseImages: ["nginx:alpine"], stack: ["react"] })).toBe("nginx");
  });

  test("language hints apply when the images name no language", () => {
    expect(id({ kind: "service", baseImages: ["alpine:3.20"], stack: ["go", "github.com/go-chi/chi"] })).toBe("go");
    expect(id({ kind: "service", image: "ghcr.io/org/api", stack: ["typescript", "node"] })).toBe("node");
    expect(id({ kind: "service", baseImages: ["debian:bookworm-slim"], stack: ["axum", "rust"] })).toBe("rust");
    expect(id({ kind: "service", baseImages: ["python:3.12"], stack: ["typescript"] })).toBe("python");
    expect(id({ kind: "service", stack: ["aspnet"] })).toBe("dotnet");
  });

  test("generic fallbacks per kind", () => {
    expect(techIconFor({ kind: "service", image: "ghcr.io/org/app:sha" })).toMatchObject({ id: "container", generic: true });
    expect(id({ kind: "service" })).toBe("container");
    expect(id({})).toBe("container");
    expect(id({ kind: "build-stage", baseImages: ["scratch"] })).toBe("build-stage");
    expect(id({ kind: "build-stage", baseImages: ["golang:1.23"] })).toBe("go");
    expect(id({ kind: "volume" })).toBe("volume");
    expect(id({ kind: "network", image: "postgres" })).toBe("network");
    expect(id({ kind: "secret" })).toBe("secret");
    expect(id({ kind: "config" })).toBe("config");
    expect(id({ kind: "external" })).toBe("external");
    expect(id({ kind: "port" })).toBe("port");
    expect(id({ kind: "something-new" })).toBe("container");
  });

  test("returns the contract shape", () => {
    const icon = techIconFor({ image: "nginx" });
    expect(Object.keys(icon).sort()).toEqual(["category", "darkInk", "generic", "id", "label", "src"]);
    expect(icon).toMatchObject({ id: "nginx", label: "NGINX", category: "web" });
  });
});
