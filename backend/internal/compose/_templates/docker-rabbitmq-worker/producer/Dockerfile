# Dependencies first (cached until requirements.txt changes), then the code.
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
RUN useradd --create-home app
WORKDIR /app
COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt
COPY producer.py ./
USER app
EXPOSE 8000
CMD ["python", "producer.py"]
