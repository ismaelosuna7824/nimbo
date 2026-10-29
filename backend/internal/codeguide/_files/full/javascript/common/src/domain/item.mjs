// Domain: plain JavaScript, no SDK, HTTP or database imports.

export class DomainError extends Error {
  /** @param {"invalid"|"not_found"|"conflict"} code */
  constructor(code, message) {
    super(message);
    this.name = "DomainError";
    this.code = code;
  }
}

// Value object: an ItemName is always valid once constructed.
export class ItemName {
  #value;
  constructor(raw) {
    if (typeof raw !== "string") throw new DomainError("invalid", "name must be a string");
    const value = raw.trim();
    if (value.length === 0 || value.length > 100) throw new DomainError("invalid", "name must be 1-100 characters");
    this.#value = value;
    Object.freeze(this);
  }
  get value() {
    return this.#value;
  }
}

export const newItem = (id, name, now) => Object.freeze({ id, name, createdAt: now.toISOString() });
