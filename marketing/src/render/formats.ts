/**
 * Output formats. The page is laid out at `width`×`height` CSS px and captured
 * at `scale` device pixels per CSS px. The landing demos keep their natural
 * size and desktop layout (their container queries switch to a stacked mobile
 * layout at ≤520 px, so every format leaves ≥540 px for the demo) and come out
 * crisp at the final resolution.
 */
export interface Format {
  id: string;
  width: number;
  height: number;
  scale: number;
}

export const FORMATS: readonly Format[] = [
  { id: "1080x1350", width: 576, height: 720, scale: 1.875 },
  { id: "1920x1080", width: 1024, height: 576, scale: 1.875 },
  { id: "1080x1080", width: 576, height: 576, scale: 1.875 },
];
