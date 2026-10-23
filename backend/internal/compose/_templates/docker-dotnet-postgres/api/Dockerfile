# Restore (cached until Api.csproj changes) and publish with the SDK, then
# run on the ASP.NET runtime image as its non-root app user.
FROM mcr.microsoft.com/dotnet/sdk:8.0-alpine AS build
WORKDIR /src
COPY Api.csproj ./
RUN --mount=type=cache,target=/root/.nuget/packages dotnet restore
COPY . .
RUN --mount=type=cache,target=/root/.nuget/packages dotnet publish -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/aspnet:8.0-alpine
WORKDIR /app
COPY --from=build /out ./
ENV ASPNETCORE_URLS=http://+:8080
EXPOSE 8080
USER app
ENTRYPOINT ["dotnet", "Api.dll"]
