import os
import time
from contextlib import asynccontextmanager

import psycopg
from fastapi import FastAPI, HTTPException, Request, Response
from prometheus_client import CONTENT_TYPE_LATEST, Histogram, generate_latest
from pydantic import BaseModel

DATABASE_URL = os.environ["DATABASE_URL"]


def connect():
    return psycopg.connect(DATABASE_URL, autocommit=True)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    with connect() as conn:
        conn.execute(
            "CREATE TABLE IF NOT EXISTS items (id SERIAL PRIMARY KEY, name TEXT NOT NULL, done BOOLEAN NOT NULL DEFAULT false)"
        )
    yield


app = FastAPI(title="items", lifespan=lifespan)

# HTTP server metrics named after the OpenTelemetry semantic conventions
# (http.server.request.duration in seconds), served at GET /metrics for
# Prometheus (compose labels prometheus.io/*).
REQUEST_DURATION = Histogram(
    "http_server_request_duration_seconds",
    "Duration of HTTP server requests.",
    ["http_request_method", "http_route", "http_response_status_code"],
    buckets=(0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10),
)


@app.middleware("http")
async def measure(request: Request, call_next):
    if request.url.path == "/metrics":
        return await call_next(request)  # not measured itself
    start = time.perf_counter()
    status = 500  # an unhandled exception is a 500 for the client
    try:
        response = await call_next(request)
        status = response.status_code
        return response
    finally:
        # The route template ("/items"), not the URL: no per-path cardinality.
        route = getattr(request.scope.get("route"), "path", "")
        REQUEST_DURATION.labels(request.method, route, str(status)).observe(time.perf_counter() - start)


@app.get("/metrics", include_in_schema=False)
def metrics():
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)


class ItemIn(BaseModel):
    name: str


@app.get("/")
def index():
    return {"service": "api", "docs": "/docs", "endpoints": ["GET /health", "GET /items", "POST /items"]}


@app.get("/health")
def health():
    try:
        with connect() as conn:
            conn.execute("SELECT 1")
    except psycopg.Error as err:
        raise HTTPException(status_code=503, detail=str(err))
    return {"status": "ok"}


@app.get("/items")
def list_items():
    with connect() as conn:
        rows = conn.execute("SELECT id, name, done FROM items ORDER BY id").fetchall()
    return [{"id": r[0], "name": r[1], "done": r[2]} for r in rows]


@app.post("/items", status_code=201)
def create_item(item: ItemIn):
    if not item.name.strip():
        raise HTTPException(status_code=400, detail="name is required")
    with connect() as conn:
        row = conn.execute("INSERT INTO items (name) VALUES (%s) RETURNING id, name, done", (item.name,)).fetchone()
    return {"id": row[0], "name": row[1], "done": row[2]}
