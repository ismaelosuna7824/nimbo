package com.example.api.domain;

/** Typed domain error; driving adapters map the code to a status. */
public final class DomainException extends RuntimeException {
  public enum Code { INVALID, NOT_FOUND, CONFLICT }

  private final Code code;

  public DomainException(Code code, String message) {
    super(message);
    this.code = code;
  }

  public Code code() {
    return code;
  }
}
