# Function app zip build (Nimbo runs: docker build --target package --output type=local,dest=dist .)
FROM node:22-alpine AS build
WORKDIR /src
COPY package*.json ./
RUN npm install --no-audit --no-fund @azure/functions@^4.6.0
COPY . .
RUN npx tsc --noEmit && npx vitest run
# @azure/functions-core is provided by the Functions worker at run time.
RUN npx esbuild src/functions/api.ts --bundle --platform=node --target=node20 --format=esm \
      --external:@azure/functions-core \
      --banner:js="import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);" \
      --outfile=/out/functions/api.mjs && \
    cp host.json /out/ && \
    printf '{"name":"api","type":"module","main":"functions/*.mjs"}\n' > /out/package.json

FROM scratch AS package
COPY --from=build /out/ /
