using System.Collections.Concurrent;
using Api.Application;
using Api.Domain;

namespace Api.Adapters.Outbound.Store;

// In-memory adapter for use-case tests and local runs.
public sealed class InMemoryItemRepository : IItemRepository
{
    private readonly ConcurrentDictionary<string, Item> _rows = new();

    public Task SaveAsync(Item item, CancellationToken ct) =>
        _rows.TryAdd(item.Id, item)
            ? Task.CompletedTask
            : throw new DomainException(DomainErrorCode.Conflict, "item already exists");

    public Task<Item?> GetAsync(string id, CancellationToken ct) =>
        Task.FromResult(_rows.TryGetValue(id, out var item) ? item : null);
}
