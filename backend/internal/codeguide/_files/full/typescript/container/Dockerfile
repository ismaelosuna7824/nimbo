FROM node:22-alpine AS build
WORKDIR /src
COPY package*.json ./
RUN npm install --no-audit --no-fund
COPY . .
RUN npx tsc --noEmit && npx vitest run
RUN npx esbuild src/server.ts --bundle --platform=node --target=node22 --format=esm \
      --banner:js="import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);" \
      --outfile=/out/server.mjs

FROM node:22-alpine AS runtime
WORKDIR /app
COPY --from=build /out/server.mjs .
USER node
EXPOSE 8080
CMD ["node", "server.mjs"]
