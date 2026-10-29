package com.example.api.application;

import com.example.api.domain.Item;
import java.util.Optional;

/** Driven port owned by the application. save throws CONFLICT when the id exists (idempotent create). */
public interface ItemRepository {
  void save(Item item);

  Optional<Item> get(String id);
}
