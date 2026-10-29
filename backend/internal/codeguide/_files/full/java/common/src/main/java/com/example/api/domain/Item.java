package com.example.api.domain;

import java.time.Instant;

/** Entity: identity plus validated state. Pure Java, no SDK or framework imports. */
public record Item(String id, ItemName name, Instant createdAt) {}
