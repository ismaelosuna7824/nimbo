using System.Text.Json;
using System.Text.RegularExpressions;
using Api.Application;
using Api.Domain;

namespace Api.Adapters.Inbound.Http;

// Driving adapter: HTTP to use cases, domain errors to status codes.
public sealed partial class Routes(Items items)
{
    public sealed record Response(int Status, string Body);

    private const int MaxBody = 16 * 1024; // OWASP API4: bound resource consumption
    private static readonly JsonSerializerOptions Json = new(JsonSerializerDefaults.Web);

    [GeneratedRegex(@"/items/([\w-]{1,64})$")]
    private static partial Regex ItemPath();

    public async Task<Response> HandleAsync(string method, string path, string? body, CancellationToken ct)
    {
        try
        {
            if (method == "GET" && path.EndsWith("/health")) return Ok(200, new { status = "ok" });
            if (method == "POST" && path.EndsWith("/items"))
            {
                if (body?.Length > MaxBody) return Error(413, "body too large");
                var input = JsonSerializer.Deserialize<CreateItemRequest>(string.IsNullOrEmpty(body) ? "{}" : body, Json);
                return Ok(201, await items.CreateAsync(input?.Name, ct));
            }
            var m = ItemPath().Match(path);
            if (method == "GET" && m.Success) return Ok(200, await items.GetAsync(m.Groups[1].Value, ct));
            return Error(404, "route not found");
        }
        catch (DomainException e)
        {
            return Error(e.Code switch
            {
                DomainErrorCode.Invalid => 400,
                DomainErrorCode.NotFound => 404,
                _ => 409,
            }, e.Message);
        }
        catch (JsonException)
        {
            return Error(400, "body must be JSON");
        }
        catch (Exception e)
        {
            Console.WriteLine(JsonSerializer.Serialize(new { level = "error", msg = "unhandled", error = e.GetType().Name }));
            return Error(500, "internal error"); // never leak internals
        }
    }

    private sealed record CreateItemRequest(string? Name);

    private static Response Ok(int status, object body) => new(status, JsonSerializer.Serialize(body, Json));
    private static Response Error(int status, string message) => Ok(status, new { error = message });
}
