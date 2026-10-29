package com.example.api.adapters.outbound.store;

import com.example.api.application.ItemRepository;
import com.example.api.domain.DomainException;
import com.example.api.domain.Item;
import com.example.api.domain.ItemName;
import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.SQLException;
import java.sql.Timestamp;
import java.util.Map;
import java.util.Optional;

/** Driven adapter: PostgreSQL over JDBC (use a pool such as HikariCP under load). */
public final class Repositories {
  private Repositories() {}

  /** Builds the repository from the env the composition root passes in (DATABASE_URL=postgres://user:password@host:port/db). */
  public static ItemRepository create(Map<String, String> env) {
    String raw = env.get("DATABASE_URL");
    if (raw == null || raw.isEmpty()) throw new IllegalStateException("DATABASE_URL is not set");
    URI u = URI.create(raw);
    String[] user = u.getRawUserInfo() == null ? new String[] {""} : u.getRawUserInfo().split(":", 2);
    String jdbc = "jdbc:postgresql://" + u.getHost() + ":" + (u.getPort() > 0 ? u.getPort() : 5432) + u.getPath();
    String username = URLDecoder.decode(user[0], StandardCharsets.UTF_8);
    String password = user.length > 1 ? URLDecoder.decode(user[1], StandardCharsets.UTF_8) : null;
    Sql sql = () -> DriverManager.getConnection(jdbc, username, password);
    try (Connection c = sql.open()) {
      c.createStatement().execute("CREATE TABLE IF NOT EXISTS items (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)");
    } catch (SQLException e) {
      throw new IllegalStateException(e);
    }
    return new ItemRepository() {
      @Override
      public void save(Item item) {
        try (Connection c = sql.open();
            var st = c.prepareStatement("INSERT INTO items (id, name, created_at) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING")) {
          st.setString(1, item.id());
          st.setString(2, item.name().value());
          st.setTimestamp(3, Timestamp.from(item.createdAt()));
          if (st.executeUpdate() == 0) throw new DomainException(DomainException.Code.CONFLICT, "item already exists");
        } catch (SQLException e) {
          throw new IllegalStateException(e);
        }
      }

      @Override
      public Optional<Item> get(String id) {
        try (Connection c = sql.open(); var st = c.prepareStatement("SELECT name, created_at FROM items WHERE id = ?")) {
          st.setString(1, id);
          try (var rs = st.executeQuery()) {
            if (!rs.next()) return Optional.empty();
            return Optional.of(new Item(id, new ItemName(rs.getString(1)), rs.getTimestamp(2).toInstant()));
          }
        } catch (SQLException e) {
          throw new IllegalStateException(e);
        }
      }
    };
  }

  private interface Sql {
    Connection open() throws SQLException;
  }
}
