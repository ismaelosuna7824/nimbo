using Api.Domain;

namespace Api.Application;

// Driven port owned by the application. SaveAsync throws Conflict when the id exists (idempotent create).
public interface IItemRepository
{
    Task SaveAsync(Item item, CancellationToken ct);
    Task<Item?> GetAsync(string id, CancellationToken ct);
}

// Plain data that crosses the boundary.
public sealed record ItemView(string Id, string Name, DateTimeOffset CreatedAt)
{
    public static ItemView Of(Item i) => new(i.Id, i.Name.Value, i.CreatedAt);
}

public sealed class Items(IItemRepository repo, Func<string> newId, TimeProvider clock)
{
    public async Task<ItemView> CreateAsync(string? name, CancellationToken ct)
    {
        var item = new Item(newId(), new ItemName(name), clock.GetUtcNow());
        await repo.SaveAsync(item, ct);
        return ItemView.Of(item);
    }

    public async Task<ItemView> GetAsync(string id, CancellationToken ct) =>
        await repo.GetAsync(id, ct) is { } item
            ? ItemView.Of(item)
            : throw new DomainException(DomainErrorCode.NotFound, "item not found");
}
