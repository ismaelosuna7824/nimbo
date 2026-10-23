# Dependencies first (cached until requirements.txt changes), then the code.
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
RUN useradd --create-home app
WORKDIR /app
COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt
COPY main.py ./
USER app
EXPOSE 8000
# uvicorn handles SIGTERM itself (graceful shutdown on docker stop).
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
