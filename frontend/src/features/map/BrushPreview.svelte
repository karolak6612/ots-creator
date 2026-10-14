<script lang="ts">
  // The whole group of tiles a stamp brush places, drawn from the item
  // textures into one canvas: each item sits at its tile with its
  // bottom-right corner on the tile's, like the client draws bigger objects.
  // One canvas has no seams between tiles; it is shown at most `max` px a
  // tile, smaller when the panel is narrower.
  import { Category, res } from "../../lib/api";
  import type { BrushPreview } from "../../lib/map/api";
  import { map } from "../../lib/map/state.svelte";
  import { versions } from "../../lib/state.svelte";

  let {
    preview,
    max = 16,
    selected = [],
    onselect,
  }: {
    preview: BrushPreview;
    max?: number;
    /** Tiles outlined as chosen (preview coordinates). */
    selected?: { x: number; y: number }[];
    /** Makes tiles selectable: a click picks one, a drag a box; add keeps
        (Ctrl toggles a clicked tile, Ctrl+drag adds a box). */
    onselect?: (tiles: { x: number; y: number }[], add: boolean) => void;
  } = $props();
  type P = { x: number; y: number };
  let hover = $state<P | null>(null);
  let drag = $state<{ from: P; to: P; add: boolean } | null>(null);
  let hit: HTMLDivElement | undefined = $state();

  function tileAt(e: MouseEvent, clamp = false): P | null {
    const r = hit!.getBoundingClientRect();
    let x = Math.floor(((e.clientX - r.left) / r.width) * preview.w);
    let y = Math.floor(((e.clientY - r.top) / r.height) * preview.h);
    if (clamp) {
      x = Math.min(Math.max(x, 0), preview.w - 1);
      y = Math.min(Math.max(y, 0), preview.h - 1);
    }
    return x >= 0 && y >= 0 && x < preview.w && y < preview.h ? { x, y } : null;
  }

  function box_(d: { from: P; to: P }) {
    return { x0: Math.min(d.from.x, d.to.x), y0: Math.min(d.from.y, d.to.y), x1: Math.max(d.from.x, d.to.x), y1: Math.max(d.from.y, d.to.y) };
  }
  const box = $derived(drag && box_(drag));

  function down(e: MouseEvent) {
    if (e.button !== 0) return;
    const t = tileAt(e);
    if (!t) return;
    e.preventDefault();
    // Leave text fields: Delete then works on the chosen tiles.
    (document.activeElement as HTMLElement | null)?.blur();
    drag = { from: t, to: t, add: e.ctrlKey || e.metaKey || e.shiftKey };
    const move = (ev: MouseEvent) => drag && (drag.to = tileAt(ev, true)!);
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      const d = drag;
      drag = null;
      if (!d) return;
      const b = box_(d);
      const single = b.x0 === b.x1 && b.y0 === b.y1;
      if (single && d.add) {
        // Ctrl+click toggles one tile.
        const on = selected.some((s) => s.x === b.x0 && s.y === b.y0);
        onselect?.(on ? selected.filter((s) => s.x !== b.x0 || s.y !== b.y0) : [...selected, { x: b.x0, y: b.y0 }], false);
        return;
      }
      const tiles: P[] = [];
      for (let y = b.y0; y <= b.y1; y++) for (let x = b.x0; x <= b.x1; x++) tiles.push({ x, y });
      onselect?.(tiles, d.add);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  }

  const T = 32;
  let width = $state(0);
  let canvas = $state<HTMLCanvasElement>();
  const tile = $derived(Math.max(4, Math.min(max, Math.floor(width / preview.w) || max)));

  const images = new Map<string, Promise<HTMLImageElement | null>>();
  function load(src: string): Promise<HTMLImageElement | null> {
    let p = images.get(src);
    if (!p) {
      p = new Promise((done) => {
        const img = new Image();
        img.onload = () => done(img);
        img.onerror = () => done(null);
        img.src = src;
      });
      images.set(src, p);
    }
    return p;
  }

  $effect(() => {
    const c = canvas;
    const pv = preview;
    const items = map.items;
    if (!c || !items) return;
    let stale = false;
    // Row by row, items bottom to top, as the client draws.
    const order = [...(pv.tiles ?? [])].filter((t) => !!t).sort((a, b) => a!.y - b!.y || a!.x - b!.x);
    // Client stacking within a tile: ground, borders, bottom, common, top.
    const rank = (id: number) => (items.is(id, "ground") ? 0 : ([3, 1, 2, 4][items.top[id]] ?? 3));
    const draws = order.flatMap((t) =>
      [...(t!.items ?? [])].map((id, k) => ({ id, k })).sort((a, b) => rank(a.id) - rank(b.id) || a.k - b.k).map(({ id }) => {
        const cid = items.client[id] ?? 0;
        // Uncropped, in the pattern of its tile: thumbnails are cropped to
        // their pixels and would not line up.
        return { t: t!, src: cid ? res.textureAt(Category.CategoryItem, cid, t!.x, t!.y, 7, versions.thing(Category.CategoryItem, cid)) : "" };
      }),
    );
    void Promise.all(draws.map((d) => (d.src ? load(d.src) : Promise.resolve(null)))).then((imgs) => {
      if (stale) return;
      const ctx = c.getContext("2d");
      if (!ctx) return;
      c.width = pv.w * T;
      c.height = pv.h * T;
      ctx.fillStyle = "#000";
      ctx.fillRect(0, 0, c.width, c.height);
      imgs.forEach((img, i) => {
        if (!img) return;
        const { t } = draws[i];
        ctx.drawImage(img, (t.x + 1) * T - img.width, (t.y + 1) * T - img.height);
      });
    });
    return () => (stale = true);
  });
</script>

<div class="host" bind:clientWidth={width}>
  <div class="box" style="width:{preview.w * tile}px;height:{preview.h * tile}px">
    <canvas bind:this={canvas}></canvas>
    {#if onselect}
      {#each selected as s}<div class="mark sel" style="left:{s.x * tile}px;top:{s.y * tile}px;width:{tile}px;height:{tile}px"></div>{/each}
      {#if box}
        <div class="mark boxed" style="left:{box.x0 * tile}px;top:{box.y0 * tile}px;width:{(box.x1 - box.x0 + 1) * tile}px;height:{(box.y1 - box.y0 + 1) * tile}px"></div>
      {:else if hover}
        <div class="mark hover" style="left:{hover.x * tile}px;top:{hover.y * tile}px;width:{tile}px;height:{tile}px"></div>
      {/if}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="hit" bind:this={hit} onmousedown={down} onmousemove={(e) => (hover = tileAt(e))} onmouseleave={() => (hover = null)}></div>
    {/if}
  </div>
</div>

<style>
  .host {
    width: 100%;
    min-width: 0;
  }
  .box {
    position: relative;
    margin: 0 auto;
  }
  canvas {
    display: block;
    width: 100%;
    height: 100%;
  }
  .hit {
    position: absolute;
    inset: 0;
    cursor: pointer;
  }
  .mark {
    position: absolute;
    pointer-events: none;
    box-sizing: border-box;
  }
  .mark.hover {
    border: 1px solid rgba(255, 255, 255, 0.6);
  }
  .mark.sel {
    background: rgba(232, 196, 94, 0.25);
    border: 1px solid var(--gold);
  }
  .mark.boxed {
    border: 1px dashed #fff;
  }
</style>
