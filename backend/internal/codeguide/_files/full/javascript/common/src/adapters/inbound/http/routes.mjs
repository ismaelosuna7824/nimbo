// Driving adapter: HTTP -> use cases. Maps typed domain errors to status codes.
import { DomainError } from "../../../domain/item.mjs";

const MAX_BODY_BYTES = 16 * 1024; // OWASP API4: bound resource consumption
const STATUS = { invalid: 400, not_found: 404, conflict: 409 };

export const routes = ({ createItem, getItem }) => async (method, path, body, log) => {
  try {
    if (method === "GET" && path.endsWith("/health")) return { status: 200, body: { status: "ok" } };
    if (method === "POST" && path.endsWith("/items")) {
      if ((body?.length ?? 0) > MAX_BODY_BYTES) return { status: 413, body: { error: "body too large" } };
      const input = JSON.parse(body || "{}");
      return { status: 201, body: await createItem({ name: input.name }) };
    }
    const match = method === "GET" ? path.match(/\/items\/([\w-]{1,64})$/) : null;
    if (match) return { status: 200, body: await getItem(match[1]) };
    return { status: 404, body: { error: "route not found" } };
  } catch (err) {
    if (err instanceof DomainError) return { status: STATUS[err.code], body: { error: err.message } };
    if (err instanceof SyntaxError) return { status: 400, body: { error: "body must be JSON" } };
    log({ level: "error", msg: "unhandled", error: String(err) });
    return { status: 500, body: { error: "internal error" } }; // never leak internals
  }
};
