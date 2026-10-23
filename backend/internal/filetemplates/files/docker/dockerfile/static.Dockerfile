# NGINX that runs as a non-root user and listens on 8080, serving the
# folder's files (.dockerignore keeps the rest out).
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY . /usr/share/nginx/html
USER 101
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1
