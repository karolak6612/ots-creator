<script lang="ts">
  import { errorMessage } from "../../../lib/api";
  import { toast } from "../../../lib/state.svelte";
  import { MapService, type Border, type Brush, type BorderRule, type Chance } from "../../../lib/map/api";
  import { loadMapData, map, reloadPalette, selectBrush } from "../../../lib/map/state.svelte";
  import Dialog from "../../../lib/ui/Dialog.svelte";
  import Icon from "../../../lib/ui/Icon.svelte";
  import NumberField from "../../../lib/ui/NumberField.svelte";
  import Select from "../../../lib/ui/Select.svelte";
  import ItemIcon from "../ItemIcon.svelte";
  import ItemPicker from "../ItemPicker.svelte";
  import TagPicker from "../../../lib/ui/TagPicker.svelte";
  import BrushPreview from "../BrushPreview.svelte";
  import { frameOf, previewOf, type Frame } from "../../../lib/map/brushpreview";

  // A new brush starts by choosing what it paints; the form then shows only
  // what that kind needs, in order, with the rarely changed parts folded.
  const TYPES = [
    { value: "ground", label: "Ground", desc: "Grass, sand, floors. Borders join it to the grounds around it." },
    { value: "wall", label: "Wall", desc: "Walls that connect by themselves, with doors and windows." },
    { value: "doodad", label: "Doodad", desc: "Trees, stones, furniture: scattered or placed as a group." },
    { value: "carpet", label: "Carpet", desc: "Fills an area with matching edges and corners." },
    { value: "table", label: "Table", desc: "Pieces that join into long tables." },
    { value: "decoration", label: "Wall decoration", desc: "Things hung on walls, following the wall shape." },
  ];
  // Visual layout of border and carpet edges: [edge, column, row].
  const EDGE_GRID: [string, number, number][] = [
    ["cnw", 0, 0], ["n", 1, 0], ["cne", 2, 0],
    ["w", 0, 1], ["e", 2, 1],
    ["csw", 0, 2], ["s", 1, 2], ["cse", 2, 2],
    ["dnw", 4, 0], ["dne", 5, 0],
    ["dsw", 4, 1], ["dse", 5, 1],
  ];
  const EDGE_LABEL: Record<string, string> = {
    n: "North", e: "East", s: "South", w: "West", cnw: "Corner NW", cne: "Corner NE", csw: "Corner SW", cse: "Corner SE",
    dnw: "Diagonal NW", dne: "Diagonal NE", dsw: "Diagonal SW", dse: "Diagonal SE", c: "Center",
  };
  const MAIN_WALLS = ["horizontal", "vertical", "corner", "pole"];
  const MORE_WALLS = ["north end", "south end", "east end", "west end", "north T", "south T", "east T", "west T", "northeast diagonal", "southwest diagonal", "southeast diagonal", "intersection", "untouchable"];
  const DOOR_TYPES = ["normal", "locked", "quest", "magic", "archway", "window", "hatch window"];
  const TABLE_PARTS = ["north", "south", "east", "west", "horizontal", "vertical", "alone"];

  const target = map.editBrush ?? { name: "", type: "ground" };
  const fromSelection = target.type === "fromSelection";
  const isNew = !target.name;

  let step = $state<"type" | "edit">(isNew && !fromSelection ? "type" : "edit");
  let b = $state<Brush>({ name: "", type: target.type === "fromSelection" ? "doodad" : target.type } as Brush);
  let borders = $state<Border[]>([]);
  /** Collections the brush is in; edited brushes start with their current ones. */
  let collections = $state<string[]>(
    isNew ? [] : (map.palette?.tilesets ?? []).filter((t) => !!t && !t.auto && [...(t.terrain ?? []), ...(t.doodads ?? [])].some((x) => x?.name === target.name)).map((t) => t!.name),
  );
  let loading = $state(!isNew);
  let saving = $state(false);
  let moreWalls = $state(false);
  /** Border being edited and its selected edge. */
  let editBorder = $state<Border | null>(null);
  let edge = $state("n");

  $effect(() => {
    void MapService.Borders().then((bs) => (borders = (bs ?? []).filter((x): x is Border => !!x)));
    if (!isNew) {
      void MapService.Brush(target.name).then(
        (x) => {
          b = JSON.parse(JSON.stringify(x));
          loading = false;
        },
        (e) => {
          toast(errorMessage(e), "error");
          map.dialog = null;
        },
      );
    }
  });

  const brushNames = $derived((map.palette?.brushes ?? []).filter((x) => !!x && x.name !== b.name).map((x) => x.name));
  const tilesets = $derived((map.palette?.tilesets ?? []).filter((t) => !!t && !t.auto).map((t) => t.name));
  const borderOptions = $derived([{ value: 0, label: "None" }, ...borders.map((x) => ({ value: x.id, label: `Border ${x.id}${x.group ? ` (group ${x.group})` : ""}` }))]);

  function chances(list: Chance[] | null | undefined): Chance[] {
    return (list ?? []).filter((c): c is Chance => !!c);
  }

  function newBorder(): Border {
    return { id: 0, edges: {}, group: 0 } as Border;
  }

  async function saveBorder(bd: Border): Promise<number> {
    const id = await MapService.SaveBorder($state.snapshot(bd) as Border);
    borders = (await MapService.Borders())?.filter((x): x is Border => !!x) ?? borders;
    return id;
  }

  const typeLabel = $derived(TYPES.find((t) => t.value === b.type)?.label ?? b.type);
  /** A brush made from a selection: one group of tiles and nothing else. */
  const stamp = $derived.by(() => {
    const alts = (b.alternatives ?? []).filter((a) => !!a);
    if (b.type !== "doodad" || alts.length !== 1 || (alts[0]!.singles ?? []).length) return null;
    const comps = (alts[0]!.composites ?? []).filter((c) => !!c);
    return comps.length === 1 ? comps[0]! : null;
  });

  // Editing a stamp: chosen tiles in preview coordinates. The frame is taken
  // once, so the preview keeps its place when edge tiles go (placement
  // offsets never change).
  type P = { x: number; y: number };
  let picked = $state<P[]>([]);
  let frame = $state<Frame | null>(null);
  $effect(() => {
    if (stamp && !frame) frame = frameOf(stamp.tiles);
  });
  const tileAt = (p: P) => (stamp?.tiles ?? []).find((t) => !!t && !t.z && frame && t.x === frame.x + p.x && t.y === frame.y + p.y) ?? null;
  type StampTile = NonNullable<ReturnType<typeof tileAt>>;
  function selectTiles(tiles: P[], add: boolean) {
    picked = add ? [...picked, ...tiles.filter((t) => !picked.some((p) => p.x === t.x && p.y === t.y))] : tiles;
  }
  function removeItem(t: StampTile, k: number) {
    t.items = (t.items ?? []).filter((_, i) => i !== k);
    if (!t.items.length && stamp) stamp.tiles = (stamp.tiles ?? []).filter((x) => x !== t);
  }
  /** Clears the chosen tiles: the map keeps what is there. */
  function clearPicked() {
    if (!stamp || !picked.length) return;
    const gone = new Set(picked.map(tileAt).filter((t) => !!t));
    stamp.tiles = (stamp.tiles ?? []).filter((x) => !gone.has(x));
    picked = [];
  }
  function onkeydown(e: KeyboardEvent) {
    if (!stamp || !picked.length || (e.key !== "Delete" && e.key !== "Backspace")) return;
    if ((e.target as HTMLElement).closest("input, textarea, select, [contenteditable]")) return;
    e.preventDefault();
    clearPicked();
  }

  function chooseType(t: string) {
    b.type = t;
    step = "edit";
  }

  /** The first item of the brush: the library icon when none is chosen. */
  function firstItem(): number {
    const ids = [
      ...chances(b.items),
      ...Object.values(b.walls ?? {}).flatMap((w) => chances(w?.items)),
      ...Object.values(b.parts ?? {}).flatMap((l) => chances(l)),
      ...(b.alternatives ?? []).flatMap((a) => (a?.singles ?? []).map((x) => x?.item)),
    ];
    return ids.find((x) => x?.id)?.id ?? 0;
  }

  /** What still stops saving, told in the footer. */
  const missing = $derived.by(() => {
    if (!b.name?.trim()) return "Give the brush a name.";
    if (fromSelection) return "";
    const has = (l: Chance[] | null | undefined) => chances(l).some((c) => c.id);
    switch (b.type) {
      case "ground":
        return has(b.items) ? "" : "Add at least one ground item.";
      case "wall":
      case "decoration":
        return ["horizontal", "vertical"].every((a) => has(b.walls?.[a]?.items)) ? "" : "Give the horizontal and vertical pieces.";
      case "doodad":
        return (b.alternatives ?? []).some((a) => (a?.singles ?? []).some((x) => x?.item?.id) || (a?.composites ?? []).length) ? "" : "Add at least one item.";
      case "carpet":
        return has(b.parts?.c) ? "" : "Pick the center piece.";
      case "table":
        return has(b.parts?.alone) ? "" : "Pick the piece of a single table.";
    }
    return "";
  });

  async function save() {
    if (fromSelection) {
      if (!b.name.trim() || map.activeId === null) return;
      saving = true;
      try {
        await MapService.DoodadFromSelection(map.activeId, b.name.trim(), $state.snapshot(collections));
        await reloadPalette();
        await loadMapData(true);
        selectBrush({ kind: "doodad", name: b.name.trim(), label: b.name.trim() });
        toast(`Doodad "${b.name}" created. Click the map to place it.`, "success");
        map.dialog = null;
      } catch (e) {
        toast(errorMessage(e), "error");
      } finally {
        saving = false;
      }
      return;
    }
    saving = true;
    try {
      if (editBorder) {
        const id = await saveBorder(editBorder);
        editBorder = null;
        void id;
      }
      const snap = $state.snapshot(b) as Brush;
      if (!snap.look) snap.look = firstItem();
      if (snap.walls) {
        for (const [k, v] of Object.entries(snap.walls)) if (!v || (!(v.items ?? []).some((c) => c?.id) && !(v.doors ?? []).some((d) => d?.id))) delete snap.walls[k];
      }
      if (snap.type !== "wall" && snap.type !== "decoration") snap.walls = {};
      if (snap.type !== "doodad") {
        snap.doodad = null;
        snap.alternatives = [];
      }
      await MapService.SaveBrush(isNew ? "" : target.name, snap, $state.snapshot(collections));
      await reloadPalette();
      await loadMapData(true);
      toast(`Saved "${snap.name}".`, "success");
      map.dialog = null;
    } catch (e) {
      toast(errorMessage(e), "error");
    } finally {
      saving = false;
    }
  }

  // ----- ground -----
  function addRule() {
    b.borders = [...(b.borders ?? []), { align: "outer", to: "all", border: 0 } as BorderRule];
  }

  async function createBorderFor(i: number) {
    editBorder = { ...newBorder() };
    pendingRule = i;
  }
  let pendingRule = $state<number | null>(null);

  async function finishBorder() {
    if (!editBorder) return;
    const id = await saveBorder(editBorder);
    if (pendingRule !== null && b.borders?.[pendingRule]) b.borders[pendingRule].border = id;
    if (pendingRule === -1) b.optional = { align: "outer", border: id } as BorderRule;
    editBorder = null;
    pendingRule = null;
  }

  function borderById(id: number | undefined): Border | undefined {
    return borders.find((x) => x.id === id);
  }

  // Walls and doodads edit nested objects: make sure they exist.
  $effect(() => {
    if (loading) return;
    if (b.type === "wall" || b.type === "decoration") {
      const walls = { ...(b.walls ?? {}) };
      let added = false;
      for (const a of [...MAIN_WALLS, ...MORE_WALLS]) {
        if (!walls[a]) {
          walls[a] = { items: [], doors: [] };
          added = true;
        }
      }
      if (added) b.walls = walls;
    }
    if (b.type === "doodad" && !b.doodad) b.doodad = { draggable: true } as NonNullable<Brush["doodad"]>;
    if (b.type === "doodad" && !(b.alternatives ?? []).length) b.alternatives = [{ singles: [], composites: [] }];
  });

  // ----- carpet and table -----
  function part(align: string): Chance[] {
    return chances(b.parts?.[align]);
  }
  function setPart(align: string, id: number) {
    b.parts ??= {};
    b.parts[align] = id ? [{ id, chance: 10 }] : [];
  }
</script>

{#snippet chanceList(list: Chance[], set: (l: Chance[]) => void, addLabel = "Add item")}
  {#each list as c, i}
    <div class="row">
      <ItemPicker value={c.id} width={250} onchange={(id) => set(list.map((x, k) => (k === i ? { ...x, id } : x)))} />
      <span class="t-label">chance</span>
      <NumberField value={c.chance} min={0} max={100000} width={50} onchange={(v) => set(list.map((x, k) => (k === i ? { ...x, chance: v } : x)))} />
      <button class="t-icon-btn danger" title="Remove" onclick={() => set(list.filter((_, k) => k !== i))}><Icon name="close" /></button>
    </div>
  {/each}
  <button class="t-btn small" class:primary={!list.length} onclick={() => set([...list, { id: 0, chance: 10 }])}>{addLabel}</button>
{/snippet}

{#snippet head(n: number, title: string, hint = "")}
  <div class="sh"><span class="st">{n}. {title}</span>{#if hint}<span class="t-label hint">{hint}</span>{/if}</div>
{/snippet}

{#snippet edgeGrid(get: (e: string) => number, sel: string, onsel: (e: string) => void, withCenter = false)}
  <div class="edges">
    {#each EDGE_GRID as [e, cx, cy]}
      <button class="slot t-slot" class:on={sel === e} style="grid-column:{cx + 1};grid-row:{cy + 1}" title={EDGE_LABEL[e]} onclick={() => onsel(e)}>
        <ItemIcon id={get(e)} />
        <span class="el">{e}</span>
      </button>
    {/each}
    {#if withCenter}
      <button class="slot t-slot" class:on={sel === "c"} style="grid-column:2;grid-row:2" title="Center" onclick={() => onsel("c")}>
        <ItemIcon id={get("c")} /><span class="el">c</span>
      </button>
    {/if}
  </div>
{/snippet}

<svelte:window {onkeydown} />

<Dialog title={fromSelection ? "Doodad from selection" : isNew ? (step === "type" ? "New brush: what does it paint?" : `New ${typeLabel.toLowerCase()} brush`) : `Brush: ${target.name}`} width={fromSelection ? 400 : 640} onclose={() => (map.dialog = null)}>
  {#if loading}
    <p class="t-label">Loading…</p>
  {:else if step === "type"}
    <div class="types">
      {#each TYPES as t}
        <button class="type" onclick={() => chooseType(t.value)}>
          <span class="tname">{t.label}</span>
          <span class="tdesc">{t.desc}</span>
        </button>
      {/each}
    </div>
  {:else}
    <div class="col form">
      {@render head(1, "Name")}
      <div class="row">
        <!-- svelte-ignore a11y_autofocus -->
        <input class="t-input grow" bind:value={b.name} placeholder={b.type === "ground" ? "e.g. grass" : b.type === "wall" ? "e.g. stone wall" : "e.g. pine trees"} autofocus />
        {#if isNew && !fromSelection}
          <span class="kind t-label">{typeLabel} · <button class="link" onclick={() => (step = "type")}>change</button></span>
        {/if}
      </div>

      {#if fromSelection}
        <p class="t-label hint">The selected items become one doodad, placed with its top-left tile at the cursor.</p>
        {#if isNew}
          <div class="row"><span class="t-label lbl">Collections</span><div class="grow"><TagPicker plain allowCustom value={collections} options={tilesets.map((t) => ({ tag: t }))} placeholder="none · type to add or create" onchange={(v) => (collections = v)} /></div></div>
        {/if}
      {:else}
        {#if b.type === "ground"}
          {@render head(2, "Ground items", "One is picked at random for each tile; chance weighs the pick.")}
          {@render chanceList(chances(b.items), (l) => (b.items = l))}

          {@render head(3, "Borders", "Optional: the edges drawn where this ground meets others.")}
          {#each b.borders ?? [] as r, i}
            {#if r}
              {@const bd = borderById(r.border)}
              <div class="rule">
                <Select value={r.align} width={100} options={[{ value: "outer", label: "Outside" }, { value: "inner", label: "Inside" }]} onchange={(v) => (r.align = v)} />
                <span class="t-label">next to</span>
                <Select value={r.to || "all"} width={130} maxItems={14} options={[{ value: "all", label: "Everything" }, { value: "none", label: "Empty tiles" }, ...brushNames.map((n) => ({ value: n, label: n }))]} onchange={(v) => (r.to = v)} />
                <Select value={r.border ?? 0} width={150} maxItems={14} options={borderOptions} onchange={(v) => (r.border = v)} />
                <span class="mini">{#each ["n", "cnw", "dnw"] as e}<ItemIcon id={bd?.edges?.[e] ?? 0} size={20} />{/each}</span>
                <button class="t-icon-btn" title="Edit this border" disabled={!bd} onclick={() => ((editBorder = bd ? JSON.parse(JSON.stringify(bd)) : null), (pendingRule = null))}><Icon name="pencil" /></button>
                <button class="t-icon-btn" title="New border for this rule" onclick={() => createBorderFor(i)}><Icon name="plus" /></button>
                <button class="t-icon-btn danger" title="Remove rule" onclick={() => (b.borders = (b.borders ?? []).filter((_, k) => k !== i))}><Icon name="close" /></button>
              </div>
            {/if}
          {/each}
          <div class="row">
            <button class="t-btn small" onclick={addRule}>Add border</button>
            {#if (b.borders ?? []).length}<span class="t-label hint">Outside: drawn on the neighbor tiles. Inside: drawn on this ground.</span>{/if}
          </div>
        {:else if b.type === "wall" || b.type === "decoration"}
          {@render head(2, "Pieces", "Walls connect by themselves. Horizontal and vertical are needed; corner and pole make them look right.")}
          {#each (moreWalls ? [...MAIN_WALLS, ...MORE_WALLS] : MAIN_WALLS).filter((a) => !!b.walls?.[a]) as align}
            {@const wp = b.walls![align]!}
            <details class="wall" open={MAIN_WALLS.includes(align) && !wp.items?.length}>
              <summary
                ><span class="cap">{align}</span> <span class="t-label">{chances(wp.items).length} items · {(wp.doors ?? []).length} doors</span>
                <span class="mini">{#each chances(wp.items).slice(0, 4) as c}<ItemIcon id={c.id} size={20} />{/each}</span></summary
              >
              {@render chanceList(chances(wp.items), (l) => (wp.items = l))}
              <div class="t-label">Doors and windows in this piece</div>
              {#each wp.doors ?? [] as d, i}
                {#if d}
                  <div class="row">
                    <ItemPicker value={d.id} width={220} onchange={(id) => (d.id = id)} />
                    <Select value={d.type} width={110} options={DOOR_TYPES.map((t) => ({ value: t, label: t }))} onchange={(v) => (d.type = v)} />
                    <label class="row"><input class="t-check" type="checkbox" bind:checked={d.open} />open</label>
                    <button class="t-icon-btn danger" title="Remove" onclick={() => (wp.doors = (wp.doors ?? []).filter((_, k) => k !== i))}><Icon name="close" /></button>
                  </div>
                {/if}
              {/each}
              <button class="t-btn small" onclick={() => (wp.doors = [...(wp.doors ?? []), { id: 0, type: "normal", open: false }])}>Add door</button>
            </details>
          {/each}
          <button class="link" onclick={() => (moreWalls = !moreWalls)}>{moreWalls ? "Fewer shapes" : "More shapes: ends, T pieces, diagonals…"}</button>
        {:else if b.type === "carpet"}
          {@render head(2, "Pieces", "Click a position, then pick its item. The center fills the inside.")}
          <div class="row top">
            {@render edgeGrid((e) => part(e)[0]?.id ?? 0, edge, (e) => (edge = e), true)}
            <div class="col">
              <span class="t-label">{EDGE_LABEL[edge]}</span>
              <ItemPicker value={part(edge)[0]?.id ?? 0} width={240} onchange={(id) => setPart(edge, id)} />
            </div>
          </div>
        {:else if b.type === "table"}
          {@render head(2, "Pieces", "Alone is a single table; the others join their neighbors.")}
          {#each ["alone", ...TABLE_PARTS.filter((t) => t !== "alone")] as tp}
            <div class="row"><span class="t-label lbl cap">{tp}</span><ItemPicker value={part(tp)[0]?.id ?? 0} width={280} onchange={(id) => setPart(tp, id)} /></div>
          {/each}
        {:else if b.type === "doodad" && stamp}
          {@const pv = previewOf(stamp.tiles, frame)}
          {@const ct = picked.length === 1 ? tileAt(picked[0]) : null}
          {@render head(2, "Group", `${(stamp.tiles ?? []).length} tiles, placed whole with the top-left tile at the cursor.`)}
          <div class="row top">
            {#if pv}<div class="stamp"><BrushPreview preview={pv} max={24} selected={picked} onselect={selectTiles} /></div>{/if}
            <div class="col tilebox">
              {#if !picked.length}
                <span class="t-label hint">Click a tile to see its items, or drag a box over several. Ctrl adds to the choice; Delete clears the chosen tiles.</span>
              {:else if picked.length > 1}
                <span class="t-label">{picked.length} tiles chosen</span>
                <button class="t-btn small danger" onclick={clearPicked}>Clear {picked.length} tiles</button>
                <span class="t-label hint">Or press Delete. Cleared tiles keep what the map has there.</span>
              {:else}
                <span class="t-label">Tile {picked[0].x + 1}, {picked[0].y + 1}</span>
                {#if ct && (ct.items ?? []).some((i) => !!i)}
                  <div class="t-panel stack">
                    {#each [...(ct.items ?? [])].reverse() as it, ri}
                      {#if it}
                        {@const k = (ct.items ?? []).length - 1 - ri}
                        <div class="m-row">
                          <span class="t-slot"><ItemIcon id={it.id} size={24} /></span>
                          <span class="m-name" title="Item {it.id}">{map.items?.name(it.id) ?? `item ${it.id}`}</span>
                          <button class="t-icon-btn danger" title="Remove from this tile" onclick={() => removeItem(ct, k)}><Icon name="close" /></button>
                        </div>
                      {/if}
                    {/each}
                  </div>
                  <button class="t-btn small danger" onclick={clearPicked}>Clear tile</button>
                {:else}
                  <span class="t-label hint">Empty: the map keeps what is there.</span>
                {/if}
              {/if}
            </div>
          </div>
          <span class="t-label hint">Tiles with a ground replace the map tile; the others are added on top of it.</span>
        {:else if b.type === "doodad"}
          {@render head(2, "Items", "One is picked at random each time you place the brush.")}
          {#each b.alternatives ?? [] as alt, ai}
            {#if alt}
              {#if (b.alternatives ?? []).length > 1}<div class="t-label">Variation {ai + 1}</div>{/if}
              {#each alt.singles ?? [] as s, si}
                {#if s}
                  <div class="row">
                    <ItemPicker value={s.item.id} width={250} onchange={(id) => (s.item.id = id)} />
                    <span class="t-label">chance</span><NumberField value={s.chance} min={0} max={100000} width={50} onchange={(v) => (s.chance = v)} />
                    <button class="t-icon-btn danger" title="Remove" onclick={() => (alt.singles = (alt.singles ?? []).filter((_, k) => k !== si))}><Icon name="close" /></button>
                  </div>
                {/if}
              {/each}
              {#each alt.composites ?? [] as c, ci}
                {#if c}
                  {@const pv = previewOf(c.tiles)}
                  <div class="row comp">
                    {#if pv}<span class="cpv"><BrushPreview preview={pv} max={6} /></span>{/if}
                    <span class="t-label">group of {(c.tiles ?? []).length} tiles · chance</span>
                    <NumberField value={c.chance} min={0} max={100000} width={50} onchange={(v) => (c.chance = v)} />
                    <button class="t-icon-btn danger" title="Remove group" onclick={() => (alt.composites = (alt.composites ?? []).filter((_, k) => k !== ci))}><Icon name="close" /></button>
                  </div>
                {/if}
              {/each}
              <div class="row">
                <button class="t-btn small" class:primary={!(alt.singles ?? []).length && !(alt.composites ?? []).length} onclick={() => (alt.singles = [...(alt.singles ?? []), { chance: 10, item: { id: 0 } }])}>Add item</button>
                {#if (b.alternatives ?? []).length > 1}<button class="t-btn small danger" onclick={() => (b.alternatives = (b.alternatives ?? []).filter((_, k) => k !== ai))}>Remove variation</button>{/if}
              </div>
            {/if}
          {/each}
          <span class="t-label hint">Groups of several tiles are made from a selection: select them on the map, then Make doodad brush.</span>
        {/if}

        {#if editBorder}
          {@const eb = editBorder}
          <div class="borderedit t-panel">
            <div class="row">
              <span class="h">{eb.id ? `Border ${eb.id}` : "New border"}</span><span class="t-label hint">Click a position, then pick its item.</span><span class="grow"></span>
              <span class="t-label">Group</span><NumberField value={eb.group ?? 0} min={0} max={65535} width={46} onchange={(v) => (eb.group = v)} />
            </div>
            <div class="row top">
              {@render edgeGrid((e) => eb.edges?.[e] ?? 0, edge, (e) => (edge = e))}
              <div class="col">
                <span class="t-label">{EDGE_LABEL[edge]}</span>
                <ItemPicker value={eb.edges?.[edge] ?? 0} width={240} onchange={(id) => (eb.edges = { ...(eb.edges ?? {}), [edge]: id })} />
                <span class="t-label hint">Empty diagonals are drawn from their two sides.</span>
              </div>
            </div>
            <div class="row">
              <button class="t-btn primary small" onclick={finishBorder}>Save border</button>
              <button class="t-btn small" onclick={() => ((editBorder = null), (pendingRule = null))}>Cancel</button>
            </div>
          </div>
        {/if}

        <!-- Rarely changed: folded so the steps above stay the focus. -->
        <details class="adv">
          <summary>More options</summary>
          <div class="col advbody">
            {#if !stamp}
              <div class="row"><span class="t-label lbl">Library icon</span><ItemPicker value={b.look ?? 0} width={260} placeholder="The first item" onchange={(id) => (b.look = id)} /></div>
            {/if}
            <div class="row"><span class="t-label lbl">Collections</span><div class="grow"><TagPicker plain allowCustom value={collections} options={tilesets.map((t) => ({ tag: t }))} placeholder="none · type to add or create" onchange={(v) => (collections = v)} /></div></div>
            {#if b.type === "ground"}
              <div class="row">
                <span class="t-label lbl">Z-order</span><NumberField value={b.zOrder ?? 0} min={-100000} max={100000} width={60} onchange={(v) => (b.zOrder = v)} />
                <span class="t-label hint">Borders of a higher z-order lie over lower ones.</span>
              </div>
              <label class="row"><input class="t-check" type="checkbox" checked={!b.noRandomize} onchange={(e) => (b.noRandomize = !e.currentTarget.checked)} />Mix the ground items randomly</label>
              <div class="row">
                <span class="t-label lbl">Optional border</span>
                <Select value={b.optional?.border ?? 0} width={150} options={borderOptions} onchange={(v) => (b.optional = v ? ({ align: "outer", border: v } as BorderRule) : null)} />
                <button class="t-icon-btn" title="New optional border" onclick={() => ((editBorder = newBorder()), (pendingRule = -1))}><Icon name="plus" /></button>
              </div>
              <div class="row">
                <span class="t-label lbl">Friends</span>
                <input
                  class="t-input grow"
                  value={(b.friends ?? []).join(", ")}
                  placeholder="brushes that get no border against this one"
                  onchange={(e) =>
                    (b.friends = e.currentTarget.value
                      .split(",")
                      .map((s) => s.trim())
                      .filter(Boolean))}
                />
              </div>
            {:else if b.type === "doodad" && b.doodad}
              {@const o = b.doodad}
              <div class="opts">
                <label class="row"><input class="t-check" type="checkbox" bind:checked={o.draggable} />Draw by dragging</label>
                <label class="row"><input class="t-check" type="checkbox" bind:checked={o.onBlocking} />Also on blocking tiles</label>
                <label class="row"><input class="t-check" type="checkbox" bind:checked={o.onDuplicate} />Allow twice on a tile</label>
                <label class="row"><input class="t-check" type="checkbox" bind:checked={o.oneSize} />One size (ignores brush size)</label>
                <label class="row"><input class="t-check" type="checkbox" bind:checked={o.redoBorders} />Redo borders (ground doodads)</label>
              </div>
              <div class="row">
                <span class="t-label lbl">Density</span>
                <NumberField value={o.thickness ?? 0} min={0} max={10000} width={50} onchange={(v) => (o.thickness = v)} /><span>/</span>
                <NumberField value={o.thicknessCeiling ?? 100} min={1} max={10000} width={50} onchange={(v) => (o.thicknessCeiling = v)} />
                <span class="t-label hint">objects per tile of a big brush</span>
              </div>
              <button class="t-btn small" onclick={() => (b.alternatives = [...(b.alternatives ?? []), { singles: [], composites: [] }])}>Add variation</button>
            {/if}
          </div>
        </details>
      {/if}
    </div>
  {/if}
  {#snippet footer()}
    {#if step === "edit"}
      {#if missing}<span class="t-label need">{missing}</span>{/if}
      <span class="grow"></span>
      <button class="t-btn primary" disabled={saving || !!missing} onclick={save}>{fromSelection ? "Create" : isNew ? "Create brush" : "Save brush"}</button>
    {/if}
    <button class="t-btn" onclick={() => (map.dialog = null)}>Cancel</button>
  {/snippet}
</Dialog>

<style>
  .form {
    gap: 6px;
    max-height: calc(100vh - 160px);
    overflow-y: auto;
    padding-right: 4px;
  }
  /* Step heading: gold like other section titles, hint on the same line. */
  .sh {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 8px;
  }
  .sh:first-child {
    margin-top: 0;
  }
  .st {
    flex: none;
    color: var(--gold);
  }
  .kind {
    flex: none;
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
  }
  .kind .link {
    align-self: center;
  }
  .types {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
  }
  .type {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 3px;
    padding: 8px 10px;
    text-align: left;
    font: inherit;
    background: rgba(0, 0, 0, 0.25);
    border: 1px solid #444;
    cursor: pointer;
  }
  .type:hover,
  .type:focus-visible {
    border-color: var(--gold);
    background: rgba(255, 255, 255, 0.05);
    outline: none;
  }
  .tname {
    color: var(--text-bright);
  }
  .tdesc {
    color: var(--text-dim);
    font-size: 12px;
  }
  .link {
    background: none;
    border: 0;
    padding: 0;
    color: var(--gold);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    text-decoration: underline;
    align-self: flex-start;
  }
  .adv {
    margin-top: 8px;
    border-top: 1px solid #333;
    padding-top: 6px;
  }
  .adv summary {
    cursor: pointer;
    color: var(--text-dim);
  }
  .adv summary:hover {
    color: var(--text-bright);
  }
  .advbody {
    gap: 6px;
    margin-top: 6px;
  }
  .need {
    align-self: center;
    color: var(--text-dim);
    font-size: 12px;
  }
  .grow {
    flex: 1;
  }
  .lbl {
    width: 100px;
    flex: none;
  }
  .hint {
    font-size: 12px;
    margin: 0;
  }
  .h {
    color: var(--gold);
  }
  .small {
    min-width: 0;
    padding: 0 8px;
    align-self: flex-start;
  }
  .rule {
    display: flex;
    gap: 4px;
    align-items: center;
    flex-wrap: wrap;
  }
  .mini {
    display: inline-flex;
    gap: 1px;
    vertical-align: middle;
  }
  .wall {
    border: 1px solid #333;
    padding: 3px 6px;
  }
  .wall summary {
    cursor: pointer;
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .wall[open] {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .cap {
    text-transform: capitalize;
  }
  .edges {
    display: grid;
    grid-template-columns: repeat(6, 36px);
    grid-template-rows: repeat(3, 36px);
    gap: 2px;
  }
  .slot {
    position: relative;
    border: 1px solid transparent;
    cursor: pointer;
    padding: 0;
  }
  .slot.on {
    outline: 1px solid var(--gold);
  }
  .el {
    position: absolute;
    left: 1px;
    top: 0;
    font: var(--fs-small) / 10px var(--font-small);
    color: var(--text-dim);
    text-shadow: 1px 1px 0 #000;
  }
  .top {
    align-items: flex-start;
    gap: 12px;
  }
  .borderedit {
    padding: 6px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .opts {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 3px;
  }
  .stamp {
    flex: 1;
    min-width: 0;
  }
  .tilebox {
    width: 220px;
    flex: none;
    gap: 4px;
  }
  .stack {
    max-height: 260px;
    overflow-y: auto;
    padding: 2px;
  }
  .cpv {
    width: 72px;
    flex: none;
  }
  .comp {
    gap: 6px;
  }
</style>
