using Api.Adapters.Inbound.Http;
using Api.Adapters.Outbound.Store;
using Api.Application;

// Composition root + HTTP server driving adapter (containers).
var routes = new Routes(new Items(Repositories.Create(Environment.GetEnvironmentVariable), () => Guid.NewGuid().ToString(), TimeProvider.System));
var app = WebApplication.CreateSlimBuilder(args).Build();

app.Run(async ctx =>
{
    var requestId = ctx.Request.Headers["X-Request-Id"].FirstOrDefault() ?? Guid.NewGuid().ToString(); // correlation id
    using var reader = new StreamReader(ctx.Request.Body);
    var res = await routes.HandleAsync(ctx.Request.Method, ctx.Request.Path.Value ?? "/", await reader.ReadToEndAsync(), ctx.RequestAborted);
    ctx.Response.StatusCode = res.Status;
    ctx.Response.ContentType = "application/json";
    ctx.Response.Headers["X-Request-Id"] = requestId;
    await ctx.Response.WriteAsync(res.Body);
    Console.WriteLine($"{{\"level\":\"info\",\"msg\":\"request\",\"requestId\":\"{requestId}\",\"status\":{res.Status}}}");
});

app.Run($"http://0.0.0.0:{Environment.GetEnvironmentVariable("PORT") ?? "8080"}");
