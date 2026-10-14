// Render data of client items and outfits, loaded in batches from
// MapService.Appearances.
import { Category } from "../api";
import { MapService, type Appearance } from "./api";

/** Appearance flags (see app.Appearance). */
export const AF = {
  animateAlways: 1,
  hangable: 2,
  hookSouth: 4,
  hookEast: 8,
  lying: 16,
  translucent: 32,
  top: 64,
  bottom: 128,
  border: 256,
  ground: 512,
  dontHide: 1024,
  fullGround: 2048,
} as const;

export interface Look extends Omit<Appearance, "s" | "ox" | "oy" | "el"> {
  s: number[];
  ox: number;
  oy: number;
  el: number;
  /** Cumulative frame end times (ms) for animated things. */
  cum: number[] | null;
  total: number;
}

function prepare(a: Appearance): Look {
  const l = a as unknown as Look;
  l.s = a.s ?? [];
  l.ox = a.ox ?? 0;
  l.oy = a.oy ?? 0;
  l.el = a.el ?? 0;
  l.cum = null;
  l.total = 0;
  if (a.f > 1) {
    const ds = a.d && a.d.length === a.f ? a.d : new Array(a.f).fill(a.f > 1 ? 500 : 0);
    let t = 0;
    l.cum = ds.map((d) => (t += Math.max(d, 50)));
    l.total = t;
  }
  return l;
}

/** Frame of an animation at time t (all instances run in sync). */
export function frameAt(l: Look, t: number): number {
  if (!l.cum) return 0;
  const m = t % l.total;
  for (let i = 0; i < l.cum.length; i++) if (m < l.cum[i]) return i;
  return 0;
}

class Cache {
  private map = new Map<number, Look | null>();
  private queued = new Set<number>();
  private timer = 0;
  constructor(
    private cat: Category,
    private onload: () => void,
  ) {}

  get(id: number): Look | null | undefined {
    const v = this.map.get(id);
    if (v === undefined && !this.queued.has(id)) {
      this.queued.add(id);
      if (!this.timer) this.timer = window.setTimeout(() => this.flush(), 0);
    }
    return v;
  }

  private async flush(): Promise<void> {
    this.timer = 0;
    const ids = [...this.queued];
    for (let i = 0; i < ids.length; i += 400) {
      const batch = ids.slice(i, i + 400);
      try {
        const list = (await MapService.Appearances(this.cat, batch)) ?? [];
        const found = new Set<number>();
        for (const a of list) {
          this.map.set(a.id, prepare(a));
          found.add(a.id);
        }
        for (const id of batch) {
          if (!found.has(id)) this.map.set(id, null);
          this.queued.delete(id);
        }
      } catch {
        for (const id of batch) this.queued.delete(id);
      }
    }
    this.onload();
  }

  clear(): void {
    this.map.clear();
  }
}

/** Item and outfit appearances. */
export class Appearances {
  readonly items: Cache;
  readonly outfits: Cache;
  constructor(onload: () => void) {
    this.items = new Cache(Category.CategoryItem, onload);
    this.outfits = new Cache(Category.CategoryOutfit, onload);
  }
  /** Bumped by clear(): what was built from older appearances is stale. */
  rev = 0;
  clear(): void {
    this.items.clear();
    this.outfits.clear();
    this.rev++;
  }
}
