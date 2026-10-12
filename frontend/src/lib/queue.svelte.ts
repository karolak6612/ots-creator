// Queues of objects waiting to be dropped into the open client (A) under
// ids chosen in its slots (DropPanel). Each drop takes the first queued
// object of the category. The compare window queues objects of the second
// client (B) by id; the object viewer queues object files by path.

import { CATEGORY_NAMES, CompareService, res, ThingService, type Category } from "./api";
import { ask } from "./confirm.svelte";
import { app, hasToast, otherVersions, run, toast } from "./state.svelte";

export interface QueueItem<K extends number | string = number | string> {
  category: Category;
  key: K;
  /** Thumbnail URL; the queue's thumb() is used when missing. */
  thumb?: string;
  /** Text shown for the item; "#key" when missing. */
  label?: string;
}

/** What a dropped batch got: ids in A, and new sprites when known. */
export interface DropResult {
  ids: number[];
  sprites?: number;
}

export interface QueueSource<K extends number | string> {
  /** Where the queued objects come from, for texts ("from B"). */
  from: string;
  /** Hint shown while the queue is empty. */
  hint: string;
  items: () => QueueItem<K>[];
  setItems: (items: QueueItem<K>[]) => void;
  thumb?: (c: Category, key: K) => string;
  /** Puts keys into A at targets, or at the end of A when targets is null. */
  put: (c: Category, keys: K[], targets: number[] | null) => Promise<DropResult>;
}

export class DropQueue<K extends number | string = number | string> {
  constructor(readonly src: QueueSource<K>) {}

  /** Queued items of category c, in queue order. */
  list(c: Category): QueueItem<K>[] {
    return this.src.items().filter((q) => q.category === c);
  }

  /** Queued keys of category c, in queue order. */
  queued(c: Category): K[] {
    return this.list(c).map((q) => q.key);
  }

  /** Total number of queued items per category. */
  counts(): Map<Category, number> {
    const m = new Map<Category, number>();
    for (const q of this.src.items()) m.set(q.category, (m.get(q.category) ?? 0) + 1);
    return m;
  }

  label(q: QueueItem<K>): string {
    return q.label ?? `#${q.key}`;
  }

  thumb(q: QueueItem<K>): string {
    return q.thumb ?? this.src.thumb?.(q.category, q.key) ?? "";
  }

  /** Adds items to the end of the queue (each one once). */
  enqueue(c: Category, items: (K | Omit<QueueItem<K>, "category">)[]): void {
    const have = new Set(this.queued(c));
    const add = items
      .map((it) => (typeof it === "object" ? { ...it, category: c } : { category: c, key: it }))
      .filter((q) => !have.has(q.key) && (have.add(q.key), true));
    this.src.setItems([...this.src.items(), ...add]);
  }

  dequeue(c: Category, keys: K[]): void {
    this.src.setItems(this.src.items().filter((q) => q.category !== c || !keys.includes(q.key)));
  }

  clear(c: Category): void {
    this.src.setItems(this.src.items().filter((q) => q.category !== c));
  }

  /** Moves a queued item to the front, so it is dropped next. */
  toFront(c: Category, key: K): void {
    const all = this.src.items();
    const i = all.findIndex((q) => q.category === c && q.key === key);
    if (i < 0) return;
    this.src.setItems([all[i], ...all.slice(0, i), ...all.slice(i + 1)]);
  }

  /**
   * Drops queued items of c into A from id at: the first n (all when n is
   * 0), into free ids from at, or into the ids in a row. at 0 appends them
   * to the end of A. Objects of A that would be replaced are confirmed
   * first. Returns the ids they got.
   */
  async drop(c: Category, at: number, n = 1, free = false): Promise<number[]> {
    const all = this.queued(c);
    const keys = n > 0 ? all.slice(0, n) : all;
    if (!keys.length) {
      toast(`The queue is empty. ${this.src.hint}`);
      return [];
    }
    let targets: number[] | null = null;
    if (at !== 0) {
      const plan = await run("Dropping", () => ThingService.PlanTargets(c, at, keys.length, free));
      if (!plan) return [];
      const taken = plan.taken ?? [];
      if (taken.length) {
        const list = taken.slice(0, 5).map((id) => `#${id}`).join(", ") + (taken.length > 5 ? "…" : "");
        if (!(await ask({ title: "Replace objects", message: `Replace ${taken.length} object(s) of A (${list})? Ctrl+Z brings them back.`, ok: "Replace" }))) return [];
      }
      targets = plan.ids ?? [];
    }
    const r = await run("Dropping", () => this.src.put(c, keys, targets));
    if (!r) return [];
    this.dequeue(c, keys);
    // Drops in a row add up in one toast while it is shown.
    if (!hasToast(DROP_TOAST) || streak.c !== c || streak.q !== this) streak = { q: this, c, ids: [], sprites: 0 };
    streak.ids.push(...r.ids);
    streak.sprites += r.sprites ?? 0;
    const sprites = r.sprites === undefined ? "" : `, ${streak.sprites} new sprite(s)`;
    toast(`Dropped ${streak.ids.length} ${CATEGORY_NAMES[c]}(s) at ${idRange(streak.ids)}${sprites}.`, "success", 3500, DROP_TOAST);
    return r.ids;
  }
}

const DROP_TOAST = "queue-drop";
let streak: { q: unknown; c: Category; ids: number[]; sprites: number } = { q: null, c: 0 as Category, ids: [], sprites: 0 };

/** Short text of ids: "#5", "#5-#8" or "#5, #9, #12 (+3)". */
function idRange(ids: number[]): string {
  const sorted = [...ids].sort((a, b) => a - b);
  if (sorted.every((id, i) => id === sorted[0] + i)) return sorted.length === 1 ? `#${sorted[0]}` : `#${sorted[0]}-#${sorted.at(-1)}`;
  const head = sorted.slice(0, 3).map((id) => `#${id}`).join(", ");
  return sorted.length > 3 ? `${head} (+${sorted.length - 3})` : head;
}

/** Objects of B queued in the compare window. */
export const compareQueue = new DropQueue<number>({
  from: "from B",
  hint: "Queue objects of B first (double click a row).",
  items: () => app.queue,
  setItems: (q) => (app.queue = q),
  thumb: (c, id) => res.otherThumb(c, id, otherVersions.thing(c, id)),
  put: async (c, ids, targets) => {
    const r = targets ? await CompareService.TransferTo(false, c, ids, targets) : await CompareService.Transfer(false, c, ids, true);
    return { ids: r.ids ?? [], sprites: r.sprites };
  },
});

/** Object files queued in the object viewer. */
export const fileQueue = new DropQueue<string>({
  from: "files",
  hint: "Queue object files first (double click a file).",
  items: () => app.fileQueue,
  setItems: (q) => (app.fileQueue = q),
  put: async (c, paths, targets) => {
    const end = app.maxId(c) + 1;
    const ids = await ThingService.ImportObjectsTo(c, paths, targets ?? paths.map((_, i) => end + i));
    return { ids: ids ?? [] };
  },
});
