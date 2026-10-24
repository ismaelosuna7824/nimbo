# No dependencies: the app uses Node's standard library only.
FROM node:22.21.0-alpine3.22
ENV NODE_ENV=production
WORKDIR /app
COPY package.json *.js ./
USER node
EXPOSE 4000
CMD ["node", "server.js"]
