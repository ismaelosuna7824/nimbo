package com.example.api;

import com.example.api.adapters.inbound.http.Routes;
import com.example.api.adapters.outbound.store.Repositories;
import com.example.api.application.Items;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.util.UUID;
import java.util.concurrent.Executors;

/** Composition root + HTTP server driving adapter (containers). */
public final class Main {
  public static void main(String[] args) throws IOException {
    Routes routes = new Routes(new Items(Repositories.create(System.getenv()), () -> UUID.randomUUID().toString(), Clock.systemUTC()));
    int port = Integer.parseInt(System.getenv().getOrDefault("PORT", "8080"));
    HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);
    server.setExecutor(Executors.newVirtualThreadPerTaskExecutor());
    server.createContext("/", ex -> {
      String requestId = ex.getRequestHeaders().getFirst("X-Request-Id"); // correlation id
      if (requestId == null) requestId = UUID.randomUUID().toString();
      byte[] in = ex.getRequestBody().readNBytes(1 << 20);
      Routes.Response res = routes.handle(ex.getRequestMethod(), ex.getRequestURI().getPath(), new String(in, StandardCharsets.UTF_8));
      byte[] out = res.body().getBytes(StandardCharsets.UTF_8);
      ex.getResponseHeaders().set("Content-Type", "application/json");
      ex.getResponseHeaders().set("X-Request-Id", requestId);
      ex.sendResponseHeaders(res.status(), out.length);
      ex.getResponseBody().write(out);
      ex.close();
      System.out.printf("{\"level\":\"info\",\"msg\":\"request\",\"requestId\":\"%s\",\"status\":%d}%n", requestId, res.status());
    });
    server.start();
    System.out.printf("{\"level\":\"info\",\"msg\":\"listening\",\"port\":%d}%n", port);
  }
}
