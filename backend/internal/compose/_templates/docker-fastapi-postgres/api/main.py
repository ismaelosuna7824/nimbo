import os
from contextlib import asynccontextmanager

import psycopg
from fastapi import FastAPI, HTTPException
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
