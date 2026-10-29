# Cloud Functions source zip (Nimbo runs: docker build --target package --output type=local,dest=dist .)
FROM node:22-alpine AS build
WORKDIR /src
COPY package*.json ./
RUN npm install --no-audit --no-fund @google-cloud/functions-framework@^3.4.0
COPY . .
RUN npx tsc --noEmit && npx vitest run
# Cloud Build installs the framework from package.json; everything else is bundled.
RUN npx esbuild src/index.ts --bundle --platform=node --target=node22 --format=esm \
      --external:@google-cloud/functions-framework \
      --banner:js="import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);" \
      --outfile=/out/index.mjs && \
    printf '{"name":"api","type":"module","main":"index.mjs","dependencies":{"@google-cloud/functions-framework":"^3.4.0"}}\n' > /out/package.json

FROM scratch AS package
COPY --from=build /out/ /
