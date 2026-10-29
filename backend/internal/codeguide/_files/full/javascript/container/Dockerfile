FROM node:22-alpine AS build
WORKDIR /src
COPY package*.json ./
RUN npm install --omit=dev --no-audit --no-fund
COPY . .
RUN node --test

FROM node:22-alpine AS runtime
WORKDIR /app
COPY --from=build /src/node_modules ./node_modules
COPY --from=build /src/package.json ./
COPY --from=build /src/src ./src
USER node
EXPOSE 8080
CMD ["node", "src/server.mjs"]
