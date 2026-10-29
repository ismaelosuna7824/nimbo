using Api.Adapters.Outbound.Store;
using Api.Application;
using Api.Domain;
using Xunit;

public class ItemsTests
{
    [Fact]
    public void NameIsTrimmedAndBounded()
    {
        Assert.Equal("pen", new ItemName("  pen ").Value);
        Assert.Throws<DomainException>(() => new ItemName(" "));
        Assert.Throws<DomainException>(() => new ItemName(new string('x', 101)));
    }

    [Fact]
    public async Task CreateThenGet()
    {
        var items = new Items(new InMemoryItemRepository(), () => "id-1", TimeProvider.System);
        var created = await items.CreateAsync("pen", CancellationToken.None);
        Assert.Equal(created, await items.GetAsync("id-1", CancellationToken.None));
        var e = await Assert.ThrowsAsync<DomainException>(() => items.GetAsync("nope", CancellationToken.None));
        Assert.Equal(DomainErrorCode.NotFound, e.Code);
    }
}
