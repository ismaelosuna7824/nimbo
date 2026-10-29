FROM mcr.microsoft.com/dotnet/sdk:8.0 AS build
WORKDIR /src
COPY . .
RUN dotnet test tests/Api.Tests && dotnet publish src/Api -c Release -o /out

FROM mcr.microsoft.com/dotnet/aspnet:8.0 AS runtime
WORKDIR /app
COPY --from=build /out/ .
USER app
EXPOSE 8080
ENTRYPOINT ["dotnet", "Api.dll"]
