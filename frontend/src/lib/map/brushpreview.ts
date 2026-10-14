import type { BrushPreview } from "./api";

type Tile = { x: number; y: number; z?: number; items?: ({ id: number } | null)[] | null } | null;
export type Frame = { x: number; y: number; w: number; h: number };

/** The area of the ground floor of a group of tiles, at most max a side. */
export function frameOf(tiles: Tile[] | null | undefined, max = 12): Frame | null {
  const flat = (tiles ?? []).filter((t) => !!t && !t.z) as NonNullable<Tile>[];
  if (!flat.length) return null;
  const x = Math.min(...flat.map((t) => t.x));
  const y = Math.min(...flat.map((t) => t.y));
  return { x, y, w: Math.min(max, Math.max(...flat.map((t) => t.x)) - x + 1), h: Math.min(max, Math.max(...flat.map((t) => t.y)) - y + 1) };
}

/** The ground floor of a group of tiles as a preview (same as the palette's), in frame (default: its own area). */
export function previewOf(tiles: Tile[] | null | undefined, frame: Frame | null = frameOf(tiles)): BrushPreview | null {
  if (!frame) return null;
  const flat = (tiles ?? []).filter((t) => !!t && !t.z) as NonNullable<Tile>[];
  return {
    w: frame.w,
    h: frame.h,
    tiles: flat
      .map((t) => ({ x: t.x - frame.x, y: t.y - frame.y, items: (t.items ?? []).filter((i) => !!i).map((i) => i!.id) }))
      .filter((t) => t.x >= 0 && t.y >= 0 && t.x < frame.w && t.y < frame.h),
  };
}
