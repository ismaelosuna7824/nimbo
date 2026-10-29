using Api.Application;
using Api.Domain;
using Npgsql;

namespace Api.Adapters.Outbound.Store;

// Driven adapter: PostgreSQL via Npgsql.
public sealed class Repositories(NpgsqlDataSource db) : IItemRepository
{
    // Builds the repository from the env the composition root passes in.
    public static IItemRepository Create(Func<string, string?> env)
    {
        var url = env("DATABASE_URL") ?? throw new InvalidOperationException("DATABASE_URL is not set");
        var db = NpgsqlDataSource.Create(ToConnectionString(url));
        using var cmd = db.CreateCommand("CREATE TABLE IF NOT EXISTS items (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)");
        cmd.ExecuteNonQuery();
        return new Repositories(db);
    }

    // Accepts postgres://user:password@host:port/db (like the other languages) or an Npgsql connection string.
    private static string ToConnectionString(string url)
    {
        if (!url.StartsWith("postgres://") && !url.StartsWith("postgresql://")) return url;
        var u = new Uri(url);
        var user = u.UserInfo.Split(':', 2);
        return new NpgsqlConnectionStringBuilder
        {
            Host = u.Host,
            Port = u.IsDefaultPort || u.Port < 0 ? 5432 : u.Port,
            Database = u.AbsolutePath.TrimStart('/'),
            Username = Uri.UnescapeDataString(user[0]),
            Password = user.Length > 1 ? Uri.UnescapeDataString(user[1]) : null,
        }.ConnectionString;
    }

    public async Task SaveAsync(Item item, CancellationToken ct)
    {
        await using var cmd = db.CreateCommand("INSERT INTO items (id, name, created_at) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING");
        cmd.Parameters.Add(new() { Value = item.Id });
        cmd.Parameters.Add(new() { Value = item.Name.Value });
        cmd.Parameters.Add(new() { Value = item.CreatedAt });
        if (await cmd.ExecuteNonQueryAsync(ct) == 0)
            throw new DomainException(DomainErrorCode.Conflict, "item already exists");
    }

    public async Task<Item?> GetAsync(string id, CancellationToken ct)
    {
        await using var cmd = db.CreateCommand("SELECT name, created_at FROM items WHERE id = $1");
        cmd.Parameters.Add(new() { Value = id });
        await using var r = await cmd.ExecuteReaderAsync(ct);
        if (!await r.ReadAsync(ct)) return null;
        return new Item(id, new ItemName(r.GetString(0)), r.GetFieldValue<DateTimeOffset>(1));
    }
}
