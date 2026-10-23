# Fetch the crates (cached until Cargo.toml/Cargo.lock change), build a
# release binary, then run it alone on a distroless image as a non-root
# user. Rename "app" to the package name in Cargo.toml. Distroless has no
# wget/curl: add a healthcheck once the binary can check itself.
FROM rust:1.82-slim AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    mkdir src && echo "fn main() {}" > src/main.rs && cargo fetch --locked && rm -rf src
COPY . .
RUN --mount=type=cache,target=/usr/local/cargo/registry --mount=type=cache,target=/src/target \
    cargo build --release --locked && mkdir -p /out && cp target/release/app /out/app

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
