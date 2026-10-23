# Dependencies first (cached until package.json changes), then the code.
FROM node:22-alpine
ENV NODE_ENV=production
WORKDIR /app
COPY package.json ./
RUN --mount=type=cache,target=/root/.npm npm install --omit=dev --no-audit --no-fund
COPY server.js ./
USER node
EXPOSE 3000
CMD ["node", "server.js"]
