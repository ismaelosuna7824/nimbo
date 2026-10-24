# Dependencies first (cached until the lockfile changes), then the code.
FROM node:22.21.0-alpine3.22
ENV NODE_ENV=production
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --omit=dev --no-audit --no-fund
COPY *.js ./
USER node
EXPOSE 3000
CMD ["node", "server.js"]
