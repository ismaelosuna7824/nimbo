using Amazon.DynamoDBv2;
using Amazon.DynamoDBv2.Model;
using Api.Application;
using Api.Domain;

namespace Api.Adapters.Outbound.Store;

// Driven adapter: DynamoDB table with string hash key "id".
public sealed class Repositories(IAmazonDynamoDB db, string table) : IItemRepository
{
    // Builds the repository from the env the composition root passes in.
    public static IItemRepository Create(Func<string, string?> env) =>
        new Repositories(new AmazonDynamoDBClient(), env("TABLE_NAME") ?? throw new InvalidOperationException("TABLE_NAME is not set"));

    public async Task SaveAsync(Item item, CancellationToken ct)
    {
        try
        {
            await db.PutItemAsync(new PutItemRequest
            {
                TableName = table,
                Item = new()
                {
                    ["id"] = new AttributeValue { S = item.Id },
                    ["name"] = new AttributeValue { S = item.Name.Value },
                    ["createdAt"] = new AttributeValue { S = item.CreatedAt.ToString("O") },
                },
                ConditionExpression = "attribute_not_exists(id)",
            }, ct);
        }
        catch (ConditionalCheckFailedException)
        {
            throw new DomainException(DomainErrorCode.Conflict, "item already exists");
        }
    }

    public async Task<Item?> GetAsync(string id, CancellationToken ct)
    {
        var res = await db.GetItemAsync(new GetItemRequest
        {
            TableName = table,
            Key = new() { ["id"] = new AttributeValue { S = id } },
            ConsistentRead = true,
        }, ct);
        if (res.Item is not { Count: > 0 } row) return null;
        return new Item(id, new ItemName(row["name"].S), DateTimeOffset.Parse(row["createdAt"].S));
    }
}
