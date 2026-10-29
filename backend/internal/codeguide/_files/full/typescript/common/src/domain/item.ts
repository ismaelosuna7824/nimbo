// Domain: pure TypeScript, no SDK, HTTP or database imports.

export type DomainErrorCode = "invalid" | "not_found" | "conflict";

export class DomainError extends Error {
  constructor(readonly code: DomainErrorCode, message: string) {
    super(message);
    this.name = "DomainError";
  }
}

// Value object: an ItemName is always valid once constructed.
export class ItemName {
  private constructor(readonly value: string) {}

  static of(raw: unknown): ItemName {
    if (typeof raw !== "string") throw new DomainError("invalid", "name must be a string");
    const value = raw.trim();
    if (value.length === 0 || value.length > 100) throw new DomainError("invalid", "name must be 1-100 characters");
    return new ItemName(value);
  }
}

export interface Item {
  readonly id: string;
  readonly name: ItemName;
  readonly createdAt: string;
}

export const newItem = (id: string, name: ItemName, now: Date): Item => ({ id, name, createdAt: now.toISOString() });
