export interface DiffLine {
  type: "context" | "add" | "del"
  text: string
}

export interface Hunk {
  oldStart: number
  oldLines: number
  newStart: number
  newLines: number
  lines: DiffLine[]
}

/** Above this many cells (changed old lines × changed new lines) the middle is shown as replaced. */
const MAX_LCS_CELLS = 4_000_000

/** Line diff of a → b (longest common subsequence after trimming the common prefix and suffix). */
export function diffLines(a: string, b: string): DiffLine[] {
  const x = a.split("\n")
  const y = b.split("\n")
  let start = 0
  while (start < x.length && start < y.length && x[start] === y[start]) start++
  let endX = x.length
  let endY = y.length
  while (endX > start && endY > start && x[endX - 1] === y[endY - 1]) {
    endX--
    endY--
  }
  const out: DiffLine[] = x.slice(0, start).map((text) => ({ type: "context", text }))
  const n = endX - start
  const m = endY - start
  if (n * m > MAX_LCS_CELLS) {
    for (let i = start; i < endX; i++) out.push({ type: "del", text: x[i] })
    for (let j = start; j < endY; j++) out.push({ type: "add", text: y[j] })
  } else {
    // lcs[i * (m + 1) + j]: LCS length of x[start+i..endX) and y[start+j..endY).
    const lcs = new Uint32Array((n + 1) * (m + 1))
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i * (m + 1) + j] =
          x[start + i] === y[start + j]
            ? lcs[(i + 1) * (m + 1) + j + 1] + 1
            : Math.max(lcs[(i + 1) * (m + 1) + j], lcs[i * (m + 1) + j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < n || j < m) {
      if (i < n && j < m && x[start + i] === y[start + j]) {
        out.push({ type: "context", text: x[start + i] })
        i++
        j++
      } else if (i < n && (j === m || lcs[(i + 1) * (m + 1) + j] >= lcs[i * (m + 1) + j + 1])) {
        out.push({ type: "del", text: x[start + i] })
        i++
      } else {
        out.push({ type: "add", text: y[start + j] })
        j++
      }
    }
  }
  for (let i = endX; i < x.length; i++) out.push({ type: "context", text: x[i] })
  return out
}

/** Groups a line diff into unified hunks with `context` unchanged lines around each change. */
export function toHunks(lines: DiffLine[], context = 3): Hunk[] {
  const hunks: Hunk[] = []
  let oldNo = 1
  let newNo = 1
  let current: Hunk | null = null
  let trailing = 0 // context lines since the last change of the current hunk
  for (let k = 0; k < lines.length; k++) {
    const line = lines[k]
    if (line.type !== "context") {
      if (!current) {
        const lead = []
        for (let b = k - 1; b >= 0 && lead.length < context && lines[b].type === "context"; b--) lead.unshift(lines[b])
        current = {
          oldStart: oldNo - lead.length,
          newStart: newNo - lead.length,
          oldLines: lead.length,
          newLines: lead.length,
          lines: lead,
        }
        hunks.push(current)
      }
      trailing = 0
      current.lines.push(line)
      if (line.type === "del") current.oldLines++
      else current.newLines++
    } else if (current) {
      // Keep context that may join the next change; close the hunk once it runs out.
      if (trailing < context * 2) {
        current.lines.push(line)
        current.oldLines++
        current.newLines++
        trailing++
      }
      if (trailing === context * 2 || k === lines.length - 1) {
        const extra = Math.max(0, trailing - context)
        current.lines.splice(current.lines.length - extra, extra)
        current.oldLines -= extra
        current.newLines -= extra
        current = null
      }
    }
    if (line.type !== "add") oldNo++
    if (line.type !== "del") newNo++
  }
  return hunks
}
