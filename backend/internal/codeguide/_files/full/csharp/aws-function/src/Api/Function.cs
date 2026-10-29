using Amazon.Lambda.APIGatewayEvents;
using Amazon.Lambda.Core;
using Amazon.Lambda.Serialization.SystemTextJson;
using Api.Adapters.Inbound.Http;
using Api.Adapters.Outbound.Store;
using Api.Application;

[assembly: LambdaSerializer(typeof(DefaultLambdaJsonSerializer))]

namespace Api;

// Composition root + Lambda driving adapter (API Gateway HTTP API, payload 2.0).
// Lambda handler string: Api::Api.Function::HandleAsync
public sealed class Function
{
    // Built once per execution environment (init phase), reused across invocations.
    private static readonly Routes Routes =
        new(new Items(Repositories.Create(Environment.GetEnvironmentVariable), () => Guid.NewGuid().ToString(), TimeProvider.System));

    public async Task<APIGatewayHttpApiV2ProxyResponse> HandleAsync(APIGatewayHttpApiV2ProxyRequest request, ILambdaContext context)
    {
        var res = await Routes.HandleAsync(request.RequestContext.Http.Method, request.RawPath, request.Body, CancellationToken.None);
        context.Logger.LogInformation($"{{\"msg\":\"request\",\"requestId\":\"{context.AwsRequestId}\",\"status\":{res.Status}}}");
        return new APIGatewayHttpApiV2ProxyResponse
        {
            StatusCode = res.Status,
            Headers = new Dictionary<string, string> { ["content-type"] = "application/json" },
            Body = res.Body,
        };
    }
}
