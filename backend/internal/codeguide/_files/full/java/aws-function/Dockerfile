# Lambda artifact build (Nimbo runs: docker build --target package --output type=local,dest=dist .)
FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY pom.xml .
RUN mvn -q -B dependency:go-offline
COPY src ./src
RUN mvn -q -B package

# Java runtimes load /var/task/lib/*.jar: the fat jar under lib/ is a valid zip layout.
FROM scratch AS package
COPY --from=build /src/target/api.jar /lib/api.jar
