# OCI Functions image (push to OCIR; oci_functions_function.image points at it).
FROM fnproject/node:20-dev AS build
WORKDIR /function
COPY package.json ./
RUN npm install --no-audit --no-fund
COPY . .
RUN node --test && npm prune --omit=dev

FROM fnproject/node:20 AS runtime
WORKDIR /function
COPY --from=build /function/node_modules ./node_modules
COPY --from=build /function/package.json ./
COPY --from=build /function/src ./src
ENTRYPOINT ["node", "src/func.mjs"]
