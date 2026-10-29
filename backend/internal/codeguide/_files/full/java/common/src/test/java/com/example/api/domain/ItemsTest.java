package com.example.api.domain;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import com.example.api.adapters.outbound.store.InMemoryItemRepository;
import com.example.api.application.Items;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import org.junit.jupiter.api.Test;

class ItemsTest {
  @Test
  void nameIsTrimmedAndBounded() {
    assertEquals("pen", new ItemName("  pen ").value());
    assertThrows(DomainException.class, () -> new ItemName(" "));
    assertThrows(DomainException.class, () -> new ItemName("x".repeat(101)));
  }

  @Test
  void createThenGet() {
    Items items = new Items(new InMemoryItemRepository(), () -> "id-1", Clock.fixed(Instant.EPOCH, ZoneOffset.UTC));
    Items.ItemView created = items.create("pen");
    assertEquals(created, items.get("id-1"));
    DomainException e = assertThrows(DomainException.class, () -> items.get("nope"));
    assertEquals(DomainException.Code.NOT_FOUND, e.code());
  }
}
