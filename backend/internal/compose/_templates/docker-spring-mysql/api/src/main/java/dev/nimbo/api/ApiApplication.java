package dev.nimbo.api;

import java.util.List;
import java.util.Map;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@RestController
public class ApiApplication {
    private final JdbcTemplate db;

    public ApiApplication(JdbcTemplate db) {
        this.db = db;
        db.execute("CREATE TABLE IF NOT EXISTS greetings (id INT AUTO_INCREMENT PRIMARY KEY, text VARCHAR(255) NOT NULL)");
    }

    public static void main(String[] args) {
        SpringApplication.run(ApiApplication.class, args);
    }

    @GetMapping("/")
    public Map<String, Object> index() {
        return Map.of("service", "api", "endpoints", List.of("GET /health", "GET /greetings", "POST /greetings {text}"));
    }

    @GetMapping("/health")
    public ResponseEntity<Map<String, String>> health() {
        try {
            db.queryForObject("SELECT 1", Integer.class);
            return ResponseEntity.ok(Map.of("status", "ok"));
        } catch (RuntimeException e) {
            return ResponseEntity.status(HttpStatus.SERVICE_UNAVAILABLE).body(Map.of("status", "error", "error", e.getMessage()));
        }
    }

    @GetMapping("/greetings")
    public List<Map<String, Object>> greetings() {
        return db.queryForList("SELECT id, text FROM greetings ORDER BY id DESC LIMIT 50");
    }

    @PostMapping("/greetings")
    public ResponseEntity<Map<String, Object>> create(@RequestBody Map<String, String> body) {
        String text = body.getOrDefault("text", "").trim();
        if (text.isEmpty()) {
            return ResponseEntity.badRequest().body(Map.of("error", "text is required"));
        }
        db.update("INSERT INTO greetings (text) VALUES (?)", text);
        return ResponseEntity.status(HttpStatus.CREATED).body(Map.of("text", text));
    }
}
