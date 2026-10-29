package com.example.api.adapters.inbound.http;

import com.example.api.application.Items;
import com.example.api.domain.DomainException;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Driving adapter: HTTP to use cases, domain errors to status codes. */
public final class Routes {
  public record Response(int status, String body) {}

  private static final int MAX_BODY = 16 * 1024; // OWASP API4: bound resource consumption
  private static final Pattern ITEM = Pattern.compile("/items/([\\w-]{1,64})$");
  private static final ObjectMapper JSON = new ObjectMapper();

  private final Items items;

  public Routes(Items items) {
    this.items = items;
  }

  public Response handle(String method, String path, String body) {
    try {
      if (method.equals("GET") && path.endsWith("/health")) return json(200, Map.of("status", "ok"));
      if (method.equals("POST") && path.endsWith("/items")) {
        if (body != null && body.length() > MAX_BODY) return error(413, "body too large");
        JsonNode in = JSON.readTree(body == null || body.isEmpty() ? "{}" : body);
        return json(201, items.create(in.path("name").asText(null)));
      }
      Matcher m = ITEM.matcher(path);
      if (method.equals("GET") && m.find()) return json(200, items.get(m.group(1)));
      return error(404, "route not found");
    } catch (DomainException e) {
      return switch (e.code()) {
        case INVALID -> error(400, e.getMessage());
        case NOT_FOUND -> error(404, e.getMessage());
        case CONFLICT -> error(409, e.getMessage());
      };
    } catch (JsonProcessingException e) {
      return error(400, "body must be JSON");
    } catch (RuntimeException e) {
      System.out.println(JSON.valueToTree(Map.of("level", "error", "msg", "unhandled", "error", String.valueOf(e))));
      return error(500, "internal error"); // never leak internals
    }
  }

  private static Response error(int status, String message) {
    return json(status, Map.of("error", message));
  }

  private static Response json(int status, Object body) {
    try {
      return new Response(status, JSON.writeValueAsString(body));
    } catch (JsonProcessingException e) {
      throw new IllegalStateException(e);
    }
  }
}
