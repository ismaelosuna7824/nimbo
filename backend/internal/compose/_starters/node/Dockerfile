# Dependencies first (cached until package.json changes), then the code.
FROM node:22-alpine
ENV NODE_ENV=production
WORKDIR /app
COPY package*.json ./
RUN --mount=type=cache,target=/root/.npm npm install --omit=dev --no-audit --no-fund
COPY . .
USER node
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1
CMD ["node", "server.js"]
