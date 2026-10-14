// Item table of the map editor: server ids with their client ids, flags,
// names and minimap colors, in typed arrays for fast lookups while drawing.
import type { ItemTable } from "./api";

export class ItemIndex {
  readonly maxId: number;
  readonly client: Uint16Array;
  readonly flags: Uint32Array;
  readonly top: Uint8Array;
  readonly minimap: Uint16Array;
  readonly kind: Uint8Array;
  readonly exists: Uint8Array;
  readonly names: string[];
  readonly ids: number[];
  readonly bits: Record<string, number>;
  readonly mode: string;
  /** Lowercase names for searching. */
  private lower: string[];

  constructor(t: ItemTable) {
    this.maxId = t.maxId ?? 0;
    const n = this.maxId + 1;
    this.client = new Uint16Array(n);
    this.flags = new Uint32Array(n);
    this.top = new Uint8Array(n);
    this.minimap = new Uint16Array(n);
    this.kind = new Uint8Array(n);
    this.exists = new Uint8Array(n);
    this.names = new Array(n);
    this.lower = new Array(n);
    this.ids = t.ids ?? [];
    this.bits = (t.flagbits ?? {}) as Record<string, number>;
    this.mode = t.mode;
    const ids = t.ids ?? [];
    for (let i = 0; i < ids.length; i++) {
      const id = ids[i];
      this.client[id] = t.clients?.[i] ?? 0;
      this.flags[id] = t.flags?.[i] ?? 0;
      this.top[id] = t.tops?.[i] ?? 0;
      this.minimap[id] = t.minimap?.[i] ?? 0;
      this.kind[id] = t.kinds?.[i] ?? 0;
      this.exists[id] = 1;
      const name = t.names?.[i] ?? "";
      this.names[id] = name;
      this.lower[id] = name.toLowerCase();
    }
  }

  has(id: number): boolean {
    return id > 0 && id <= this.maxId && this.exists[id] === 1;
  }

  is(id: number, flag: string): boolean {
    return id <= this.maxId && (this.flags[id] & (this.bits[flag] ?? 0)) !== 0;
  }

  name(id: number): string {
    const n = id <= this.maxId ? this.names[id] : "";
    return n || `item ${id}`;
  }

  /** Items whose name contains q or whose id is q, ids first. */
  search(q: string, limit = 500): number[] {
    const s = q.trim().toLowerCase();
    if (!s) return [];
    const out: number[] = [];
    const num = /^\d+$/.test(s) ? Number(s) : NaN;
    if (this.has(num)) out.push(num);
    // "100-200" lists an id range.
    const range = s.match(/^(\d+)\s*-\s*(\d+)$/);
    if (range) {
      for (let id = Number(range[1]); id <= Math.min(Number(range[2]), this.maxId) && out.length < limit; id++) {
        if (this.has(id) && id !== num) out.push(id);
      }
      return out;
    }
    for (const id of this.ids) {
      if (out.length >= limit) break;
      if (id !== num && this.lower[id]?.includes(s)) out.push(id);
    }
    return out;
  }
}

/** Tibia minimap color index to CSS rgb. */
export function minimapCss(c: number): string {
  if (c >= 216) return "#000";
  return `rgb(${Math.floor(c / 36) % 6 * 51},${Math.floor(c / 6) % 6 * 51},${(c % 6) * 51})`;
}

/** Tibia minimap color index to 0xRRGGBB. */
export function minimapRgb(c: number): [number, number, number] {
  if (c >= 216 || c <= 0) return [0, 0, 0];
  return [(Math.floor(c / 36) % 6) * 51, (Math.floor(c / 6) % 6) * 51, (c % 6) * 51];
}
