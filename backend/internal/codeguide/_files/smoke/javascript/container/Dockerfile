FROM node:22-alpine AS build
WORKDIR /src
COPY package.json ./
RUN npm install --omit=dev --no-audit --no-fund

FROM node:22-alpine AS runtime
WORKDIR /app
COPY --from=build /src/node_modules ./node_modules
COPY package.json ./
COPY src/ ./src/
USER node
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/health || exit 1
CMD ["node", "src/server.mjs"]
