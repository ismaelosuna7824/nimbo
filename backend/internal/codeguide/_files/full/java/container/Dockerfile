FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY pom.xml .
RUN mvn -q -B dependency:go-offline
COPY src ./src
RUN mvn -q -B package

FROM eclipse-temurin:21-jre-alpine AS runtime
WORKDIR /app
COPY --from=build /src/target/api.jar .
USER 1000
EXPOSE 8080
CMD ["java", "-XX:MaxRAMPercentage=75", "-jar", "api.jar"]
