# Build the jar with Gradle (dependencies cached until the build scripts
# change), then run it on a JRE-only image as a non-root user. The *-plain
# jar Spring Boot also writes is skipped.
FROM gradle:8.10-jdk21-alpine AS build
WORKDIR /src
COPY settings.gradle* build.gradle* ./
RUN --mount=type=cache,target=/home/gradle/.gradle gradle dependencies --no-daemon -q
COPY src ./src
RUN --mount=type=cache,target=/home/gradle/.gradle gradle build -x test --no-daemon -q \
    && mkdir -p /out \
    && find build/libs -name '*.jar' ! -name '*-plain.jar' -exec cp {} /out/app.jar \;

FROM eclipse-temurin:21-jre-alpine
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/app.jar ./app.jar
USER app
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1
ENTRYPOINT ["java", "-XX:MaxRAMPercentage=75", "-jar", "/app/app.jar"]
