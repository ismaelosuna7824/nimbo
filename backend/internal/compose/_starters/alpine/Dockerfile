FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
# Replace with your program (exec form, so it receives SIGTERM directly).
CMD ["sh", "-c", "echo ready && exec sleep infinity"]
