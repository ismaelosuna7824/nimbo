# OCI Functions image (push to OCIR; oci_functions_function.image points at it).
FROM fnproject/node:20-dev AS build
WORKDIR /function
COPY package.json ./
RUN npm install --omit=dev --no-audit --no-fund

FROM fnproject/node:20 AS runtime
WORKDIR /function
COPY --from=build /function/node_modules ./node_modules
COPY package.json ./
COPY src/ ./src/
ENTRYPOINT ["node", "src/func.mjs"]
