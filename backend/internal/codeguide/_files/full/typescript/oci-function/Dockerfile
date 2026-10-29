# OCI Functions image (push to OCIR; oci_functions_function.image points at it).
FROM node:22-alpine AS build
WORKDIR /src
COPY package*.json ./
RUN npm install --no-audit --no-fund @fnproject/fdk@^0.0.113
COPY . .
RUN npx tsc --noEmit && npx vitest run
RUN npx esbuild src/func.ts --bundle --platform=node --target=node20 --format=esm \
      --banner:js="import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);" \
      --outfile=/out/func.mjs

FROM fnproject/node:20 AS runtime
WORKDIR /function
COPY --from=build /out/func.mjs .
ENTRYPOINT ["node", "func.mjs"]
