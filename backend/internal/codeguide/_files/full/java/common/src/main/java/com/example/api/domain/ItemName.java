package com.example.api.domain;

/** Value object: valid once constructed. */
public record ItemName(String value) {
  public ItemName {
    value = value == null ? "" : value.strip();
    if (value.isEmpty() || value.length() > 100) {
      throw new DomainException(DomainException.Code.INVALID, "name must be 1-100 characters");
    }
  }
}
