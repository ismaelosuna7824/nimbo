// A small Redis client (RESP2 over one pipelined TCP connection), enough
// for GET/SET/DEL/PING. It reconnects on its own; commands sent while the
// connection is down fail at once so callers can fall back.
import net from "node:net"

export class Redis {
  constructor(url) {
    const u = new URL(url)
    this.host = u.hostname
    this.port = Number(u.port) || 6379
    this.pending = []
    this.buf = Buffer.alloc(0)
    this.ready = false
    this.connect()
  }

  connect() {
    const sock = net.createConnection({ host: this.host, port: this.port })
    sock.setNoDelay(true)
    sock.on("connect", () => (this.ready = true))
    sock.on("data", (d) => {
      this.buf = this.buf.length ? Buffer.concat([this.buf, d]) : d
      this.drain()
    })
    sock.on("error", () => {})
    sock.on("close", () => {
      this.ready = false
      const pending = this.pending
      this.pending = []
      this.buf = Buffer.alloc(0)
      for (const p of pending) p.reject(new Error("redis connection closed"))
      setTimeout(() => this.connect(), 500)
    })
    this.sock = sock
  }

  command(...args) {
    if (!this.ready) return Promise.reject(new Error("redis unavailable"))
    return new Promise((resolve, reject) => {
      this.pending.push({ resolve, reject })
      this.sock.write(encode(args))
    })
  }

  drain() {
    while (this.pending.length) {
      const r = parse(this.buf, 0)
      if (!r) return
      this.buf = this.buf.subarray(r.end)
      const p = this.pending.shift()
      if (r.error) p.reject(new Error(r.value))
      else p.resolve(r.value)
    }
  }
}

function encode(args) {
  let out = `*${args.length}\r\n`
  for (const a of args) {
    const s = String(a)
    out += `$${Buffer.byteLength(s)}\r\n${s}\r\n`
  }
  return out
}

// parse reads one reply at offset i; null while it is incomplete.
function parse(buf, i) {
  const nl = buf.indexOf("\r\n", i)
  if (nl < 0) return null
  const type = String.fromCharCode(buf[i])
  const line = buf.toString("utf8", i + 1, nl)
  switch (type) {
    case "+":
      return { value: line, end: nl + 2 }
    case "-":
      return { value: line, error: true, end: nl + 2 }
    case ":":
      return { value: Number(line), end: nl + 2 }
    case "$": {
      const n = Number(line)
      if (n < 0) return { value: null, end: nl + 2 }
      if (buf.length < nl + 2 + n + 2) return null
      return { value: buf.toString("utf8", nl + 2, nl + 2 + n), end: nl + 2 + n + 2 }
    }
    case "*": {
      const n = Number(line)
      if (n < 0) return { value: null, end: nl + 2 }
      const items = []
      let end = nl + 2
      for (let k = 0; k < n; k++) {
        const r = parse(buf, end)
        if (!r) return null
        items.push(r.value)
        end = r.end
      }
      return { value: items, end }
    }
  }
  throw new Error(`unexpected redis reply type ${type}`)
}
