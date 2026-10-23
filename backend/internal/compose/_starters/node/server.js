const http = require("node:http");

const port = Number(process.env.PORT ?? 8080);
http
  .createServer((req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ message: "Hello from Node.js", path: req.url }));
  })
  .listen(port, () => console.log(`listening on :${port}`));
