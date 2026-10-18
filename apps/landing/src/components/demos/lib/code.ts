/** Build-time HCL / JSON line highlighters for the demo code panes (return escaped HTML). */
const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

const TOKEN =
  /("(?:[^"\\]|\\.)*")|^(\s*)(resource|module|variable|output|data|provider)\b|^(\s*)([a-z_][a-z0-9_]*)(\s*=)|^(\s*)([a-z_][a-z0-9_]*)(\s*\{)|\b((?:var|module|local|aws|azurerm|google|oci)[a-z0-9_]*\.[a-z0-9_.]+)|(#.*$)|\b(\d+(?:\.\d+)?)\b/g;

export function hclLine(line: string): string {
  let out = "";
  let last = 0;
  for (const m of line.matchAll(TOKEN)) {
    const [all, str, ws1, kw, ws2, attr, eq, ws3, block, brace, ref, comment, num] = m;
    out += esc(line.slice(last, m.index));
    last = m.index + all.length;
    if (str) out += `<span class="t-str">${esc(str)}</span>`;
    else if (kw) out += `${ws1}<span class="t-kw">${kw}</span>`;
    else if (attr) out += `${ws2}<span class="t-attr">${attr}</span>${eq}`;
    else if (block) out += `${ws3}<span class="t-kw">${block}</span>${brace}`;
    else if (ref) out += `<span class="t-ref">${esc(ref)}</span>`;
    else if (comment) out += `<span class="t-com">${esc(comment)}</span>`;
    else if (num) out += `<span class="t-num">${num}</span>`;
    else out += esc(all);
  }
  return out + esc(line.slice(last));
}

const JSON_TOKEN = /("(?:[^"\\]|\\.)*")(\s*:)?|\b(-?\d+(?:\.\d+)?|true|false|null)\b/g;

export function jsonLine(line: string): string {
  let out = "";
  let last = 0;
  for (const m of line.matchAll(JSON_TOKEN)) {
    const [all, str, colon, lit] = m;
    out += esc(line.slice(last, m.index));
    last = m.index + all.length;
    if (str && colon) out += `<span class="t-attr">${esc(str)}</span>${colon}`;
    else if (str) out += `<span class="t-str">${esc(str)}</span>`;
    else out += `<span class="t-num">${lit}</span>`;
  }
  return out + esc(line.slice(last));
}
