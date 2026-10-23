# Restore (cached until the .csproj changes) and publish with the SDK, then
# run on the ASP.NET runtime image as its non-root app user. Rename App.dll
# to your project's assembly name.
FROM mcr.microsoft.com/dotnet/sdk:8.0-alpine AS build
WORKDIR /src
COPY *.csproj ./
RUN --mount=type=cache,target=/root/.nuget/packages dotnet restore
COPY . .
RUN --mount=type=cache,target=/root/.nuget/packages dotnet publish -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/aspnet:8.0-alpine
WORKDIR /app
COPY --from=build /out ./
ENV ASPNETCORE_URLS=http://+:8080
USER app
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1
ENTRYPOINT ["dotnet", "App.dll"]
