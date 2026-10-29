package com.example.api.application;

import com.example.api.domain.DomainException;
import com.example.api.domain.Item;
import com.example.api.domain.ItemName;
import java.time.Clock;
import java.util.function.Supplier;

/** Use cases. ItemView is the plain data that crosses the boundary. */
public final class Items {
  public record ItemView(String id, String name, String createdAt) {
    static ItemView of(Item i) {
      return new ItemView(i.id(), i.name().value(), i.createdAt().toString());
    }
  }

  private final ItemRepository repo;
  private final Supplier<String> newId;
  private final Clock clock;

  public Items(ItemRepository repo, Supplier<String> newId, Clock clock) {
    this.repo = repo;
    this.newId = newId;
    this.clock = clock;
  }

  public ItemView create(String name) {
    Item item = new Item(newId.get(), new ItemName(name), clock.instant());
    repo.save(item);
    return ItemView.of(item);
  }

  public ItemView get(String id) {
    return repo.get(id)
        .map(ItemView::of)
        .orElseThrow(() -> new DomainException(DomainException.Code.NOT_FOUND, "item not found"));
  }
}
