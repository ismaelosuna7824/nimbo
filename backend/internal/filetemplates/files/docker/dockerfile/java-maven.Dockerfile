# Build the jar with Maven (dependencies cached until pom.xml changes),
# then run it on a JRE-only image as a non-root user. Expects one runnable
# jar in target/ (e.g. Spring Boot's repackaged jar).
FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY pom.xml ./
RUN --mount=type=cache,target=/root/.m2 mvn -q -B dependency:go-offline
COPY src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -q -B -DskipTests package \
    && mkdir -p /out && cp target/*.jar /out/app.jar

FROM eclipse-temurin:21-jre-alpine
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/app.jar ./app.jar
USER app
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1
ENTRYPOINT ["java", "-XX:MaxRAMPercentage=75", "-jar", "/app/app.jar"]
