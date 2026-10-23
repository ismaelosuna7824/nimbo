# Build the jar with Maven (dependencies cached until pom.xml changes),
# then run it on a JRE-only image as a non-root user.
FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY pom.xml ./
RUN --mount=type=cache,target=/root/.m2 mvn -q -B dependency:go-offline
COPY src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -q -B -DskipTests package && cp target/api.jar /api.jar

FROM eclipse-temurin:21-jre-alpine
RUN adduser -D -u 10001 app
COPY --from=build /api.jar /app/api.jar
USER app
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "/app/api.jar"]
