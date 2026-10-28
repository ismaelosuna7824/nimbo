// gifenc ships no types; the subset the animated export uses.
declare module "gifenc" {
  export type Palette = number[][]
  export interface FrameOptions {
    palette?: Palette
    delay?: number
    repeat?: number
    transparent?: boolean
    transparentIndex?: number
    dispose?: number
  }
  export interface Encoder {
    writeFrame(index: Uint8Array, width: number, height: number, opts?: FrameOptions): void
    finish(): void
    bytes(): Uint8Array<ArrayBuffer>
    bytesView(): Uint8Array
  }
  export function GIFEncoder(opts?: { initialCapacity?: number; auto?: boolean }): Encoder
  export function quantize(rgba: Uint8Array | Uint8ClampedArray, maxColors: number, opts?: { format?: "rgb565" | "rgb444" | "rgba4444" }): Palette
  export function nearestColorIndex(palette: Palette, pixel: number[]): number
}
