using Npgsql;

var builder = WebApplication.CreateBuilder(args);
var connectionString = builder.Configuration.GetConnectionString("Default")
    ?? throw new InvalidOperationException("ConnectionStrings__Default is not set");
builder.Services.AddSingleton(NpgsqlDataSource.Create(connectionString));
var app = builder.Build();

var db = app.Services.GetRequiredService<NpgsqlDataSource>();
await using (var cmd = db.CreateCommand("CREATE TABLE IF NOT EXISTS todos (id SERIAL PRIMARY KEY, title TEXT NOT NULL)"))
{
    await cmd.ExecuteNonQueryAsync();
}

app.MapGet("/", () => new { service = "api", endpoints = new[] { "GET /health", "GET /todos", "POST /todos {title}" } });

app.MapGet("/health", async (NpgsqlDataSource ds) =>
{
    try
    {
        await using var cmd = ds.CreateCommand("SELECT 1");
        await cmd.ExecuteScalarAsync();
        return Results.Ok(new { status = "ok" });
    }
    catch (Exception e)
    {
        return Results.Json(new { status = "error", error = e.Message }, statusCode: 503);
    }
});

app.MapGet("/todos", async (NpgsqlDataSource ds) =>
{
    var todos = new List<object>();
    await using var cmd = ds.CreateCommand("SELECT id, title FROM todos ORDER BY id");
    await using var reader = await cmd.ExecuteReaderAsync();
    while (await reader.ReadAsync())
    {
        todos.Add(new { id = reader.GetInt32(0), title = reader.GetString(1) });
    }
    return todos;
});

app.MapPost("/todos", async (NpgsqlDataSource ds, TodoIn input) =>
{
    if (string.IsNullOrWhiteSpace(input.Title))
    {
        return Results.BadRequest(new { error = "title is required" });
    }
    await using var cmd = ds.CreateCommand("INSERT INTO todos (title) VALUES ($1) RETURNING id");
    cmd.Parameters.AddWithValue(input.Title);
    var id = (int)(await cmd.ExecuteScalarAsync())!;
    return Results.Created($"/todos/{id}", new { id, title = input.Title });
});

app.Run();

record TodoIn(string Title);
