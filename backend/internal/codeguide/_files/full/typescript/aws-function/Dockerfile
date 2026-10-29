# Lambda artifact build (Nimbo runs: docker build --target package --output type=local,dest=dist .)
FROM node:22-alpine AS build
WORKDIR /src
COPY package*.json ./
RUN npm install --no-audit --no-fund
COPY . .
RUN npx tsc --noEmit && npx vitest run
# The nodejs22.x runtime ships the AWS SDK v3: keep it out of the bundle.
RUN npx esbuild src/handler.ts --bundle --platform=node --target=node22 --format=esm \
      --external:@aws-sdk/* --outfile=/out/index.mjs

FROM scratch AS package
COPY --from=build /out/ /
