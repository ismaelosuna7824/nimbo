# Lambda artifact build (Nimbo runs: docker build --target package --output type=local,dest=dist .)
# Framework-dependent publish for the managed dotnet8 runtime (handler Api::Api.Function::HandleAsync).
FROM mcr.microsoft.com/dotnet/sdk:8.0 AS build
WORKDIR /src
COPY . .
RUN dotnet test tests/Api.Tests && \
    dotnet publish src/Api -c Release -r linux-$(uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/') --self-contained false -o /out

FROM scratch AS package
COPY --from=build /out/ /
