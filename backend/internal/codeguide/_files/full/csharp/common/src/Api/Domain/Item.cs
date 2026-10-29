namespace Api.Domain;

// Pure C#: no SDK, HTTP or database references in Domain.
public enum DomainErrorCode { Invalid, NotFound, Conflict }

public sealed class DomainException(DomainErrorCode code, string message) : Exception(message)
{
    public DomainErrorCode Code { get; } = code;
}

// Value object: valid once constructed.
public sealed record ItemName
{
    public string Value { get; }

    public ItemName(string? raw)
    {
        var value = raw?.Trim() ?? "";
        if (value.Length is 0 or > 100)
            throw new DomainException(DomainErrorCode.Invalid, "name must be 1-100 characters");
        Value = value;
    }
}

public sealed record Item(string Id, ItemName Name, DateTimeOffset CreatedAt);
