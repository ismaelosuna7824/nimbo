package com.example.api.adapters.outbound.store;

import com.example.api.application.ItemRepository;
import com.example.api.domain.DomainException;
import com.example.api.domain.Item;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;

/** In-memory adapter for use-case tests and local runs. */
public final class InMemoryItemRepository implements ItemRepository {
  private final Map<String, Item> rows = new ConcurrentHashMap<>();

  @Override
  public void save(Item item) {
    if (rows.putIfAbsent(item.id(), item) != null) {
      throw new DomainException(DomainException.Code.CONFLICT, "item already exists");
    }
  }

  @Override
  public Optional<Item> get(String id) {
    return Optional.ofNullable(rows.get(id));
  }
}
