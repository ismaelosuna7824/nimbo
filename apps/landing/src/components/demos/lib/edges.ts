/**
 * Edge geometry for the mini canvases: bezier paths between `[data-node]`
 * cards, measured from layout offsets (CSS transforms used by entrance
 * animations are ignored, so paths never jump mid-animation).
 */

export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Layout box of `el` relative to `ancestor` (which must be positioned). */
export function boxIn(el: HTMLElement, ancestor: HTMLElement): Box {
  let x = 0;
  let y = 0;
  let n: HTMLElement | null = el;
  while (n && n !== ancestor) {
    x += n.offsetLeft;
    y += n.offsetTop;
    n = n.offsetParent as HTMLElement | null;
  }
  return { x, y, w: el.offsetWidth, h: el.offsetHeight };
}

/** Bezier between two boxes: side to side when they are apart horizontally, otherwise top/bottom. */
export function edgePath(a: Box, b: Box): string {
  if (b.x >= a.x + a.w + 20 || a.x >= b.x + b.w + 20) {
    const dir = b.x > a.x ? 1 : -1;
    const sx = dir > 0 ? a.x + a.w : a.x;
    const tx = dir > 0 ? b.x : b.x + b.w;
    const sy = a.y + a.h / 2;
    const ty = b.y + b.h / 2;
    const c = Math.max(Math.abs(tx - sx) * 0.5, 24) * dir;
    return `M${sx} ${sy}C${sx + c} ${sy} ${tx - c} ${ty} ${tx} ${ty}`;
  }
  const dir = b.y > a.y ? 1 : -1;
  const sx = a.x + a.w / 2;
  const tx = b.x + b.w / 2;
  const sy = dir > 0 ? a.y + a.h : a.y;
  const ty = dir > 0 ? b.y : b.y + b.h;
  const c = Math.max(Math.abs(ty - sy) * 0.5, 18) * dir;
  return `M${sx} ${sy}C${sx} ${sy + c} ${tx} ${ty - c} ${tx} ${ty}`;
}

/** Recomputes every `[data-edge="from>to"]` group inside `canvas` and its traveling dot. */
export function layoutEdges(canvas: HTMLElement): void {
  for (const g of canvas.querySelectorAll<SVGGElement>("[data-edge]")) {
    const key = g.dataset.edge ?? "";
    const [from, to] = key.split(">");
    const a = canvas.querySelector<HTMLElement>(`[data-node="${from}"]`);
    const b = canvas.querySelector<HTMLElement>(`[data-node="${to}"]`);
    if (!a || !b || a.getClientRects().length === 0 || b.getClientRects().length === 0) continue;
    const d = edgePath(boxIn(a, canvas), boxIn(b, canvas));
    for (const p of g.querySelectorAll("path")) p.setAttribute("d", d);
    const dot = canvas.querySelector<HTMLElement>(`[data-edge-dot="${key}"]`);
    if (dot) dot.style.offsetPath = `path("${d}")`;
  }
}
