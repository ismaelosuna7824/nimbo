package com.example.api.adapters.outbound.store;

import com.example.api.application.ItemRepository;
import com.example.api.domain.DomainException;
import com.example.api.domain.Item;
import com.example.api.domain.ItemName;
import java.time.Instant;
import java.util.Map;
import java.util.Optional;
import software.amazon.awssdk.http.urlconnection.UrlConnectionHttpClient;
import software.amazon.awssdk.services.dynamodb.DynamoDbClient;
import software.amazon.awssdk.services.dynamodb.model.AttributeValue;
import software.amazon.awssdk.services.dynamodb.model.ConditionalCheckFailedException;

/** Driven adapter: DynamoDB table with string hash key "id". */
public final class Repositories {
  private Repositories() {}

  /** Builds the repository from the env the composition root passes in. */
  public static ItemRepository create(Map<String, String> env) {
    String table = env.get("TABLE_NAME");
    if (table == null || table.isEmpty()) throw new IllegalStateException("TABLE_NAME is not set");
    DynamoDbClient db = DynamoDbClient.builder().httpClient(UrlConnectionHttpClient.create()).build();
    return new ItemRepository() {
      @Override
      public void save(Item item) {
        try {
          db.putItem(r -> r.tableName(table)
              .item(Map.of(
                  "id", AttributeValue.fromS(item.id()),
                  "name", AttributeValue.fromS(item.name().value()),
                  "createdAt", AttributeValue.fromS(item.createdAt().toString())))
              .conditionExpression("attribute_not_exists(id)"));
        } catch (ConditionalCheckFailedException e) {
          throw new DomainException(DomainException.Code.CONFLICT, "item already exists");
        }
      }

      @Override
      public Optional<Item> get(String id) {
        var out = db.getItem(r -> r.tableName(table).key(Map.of("id", AttributeValue.fromS(id))).consistentRead(true));
        if (!out.hasItem() || out.item().isEmpty()) return Optional.empty();
        var row = out.item();
        return Optional.of(new Item(id, new ItemName(row.get("name").s()), Instant.parse(row.get("createdAt").s())));
      }
    };
  }
}
