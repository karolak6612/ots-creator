<script lang="ts">
  // What to draw with: brushes, items and creatures. The search field filters
  // the open tab; the tabs show how many of theirs match.
  import { errorMessage } from "../../lib/api";
  import { ask, askText } from "../../lib/confirm.svelte";
  import { openContextMenu } from "../../lib/menu.svelte";
  import { toast } from "../../lib/state.svelte";
  import { MapService, SpawnFormat, type CreatureType, type PaletteBrush } from "../../lib/map/api";
  import { mapCommands } from "../../lib/map/commands";
  import { creatureOutfit, isBrushActive, map, reloadPalette, selectBrush, type BrushSel } from "../../lib/map/state.svelte";
  import Icon from "../../lib/ui/Icon.svelte";
  import SearchField from "../../lib/ui/SearchField.svelte";
  import Select from "../../lib/ui/Select.svelte";
  import VirtualGrid from "../../lib/ui/VirtualGrid.svelte";
  import BrushPreview from "./BrushPreview.svelte";
  import ItemIcon from "./ItemIcon.svelte";

  type View = "terrain" | "decor" | "items" | "creatures";
  const VIEWS: { id: View; label: string; title: string }[] = [
    { id: "terrain", label: "Terrain", title: "Grounds, walls, carpets and tables: they join their neighbors" },
    { id: "decor", label: "Decor", title: "Doodads: trees, stones, furniture, scattered by the brush" },
    { id: "items", label: "Items", title: "Every item of the client, placed as is" },
    { id: "creatures", label: "Creatures", title: "Monsters and NPCs" },
  ];
  type ItemKind = "all" | "ground" | "edge" | "container" | "pickup" | "other";
  const ITEM_KINDS: { id: ItemKind; label: string; title: string }[] = [
    { id: "all", label: "All", title: "All items" },
    { id: "ground", label: "Ground", title: "Ground tiles" },
    { id: "edge", label: "On top", title: "Walls, borders and other items that stay on top" },
    { id: "container", label: "Boxes", title: "Containers" },
    { id: "pickup", label: "Loot", title: "Items players can pick up" },
    { id: "other", label: "Other", title: "Everything else: decoration, furniture" },
  ];
  const TERRAIN_GROUPS: { kinds: string[]; label: string }[] = [
    { kinds: ["ground"], label: "Grounds" },
    { kinds: ["wall", "decoration"], label: "Walls" },
    { kinds: ["carpet"], label: "Carpets" },
    { kinds: ["table"], label: "Tables" },
  ];

  const PREFS_KEY = "map:library";
  const prefs = loadPrefs();
  let view = $state<View>(prefs.view);
  let collection = $state<string>(prefs.collection);
  let itemKind = $state<ItemKind>("all");
  let who = $state<"all" | "monster" | "npc">("all");
  let query = $state("");

  function loadPrefs(): { view: View; collection: string } {
    try {
      const v = JSON.parse(localStorage.getItem(PREFS_KEY) ?? "{}");
      return { view: VIEWS.some((x) => x.id === v.view) ? v.view : "terrain", collection: typeof v.collection === "string" ? v.collection : "" };
    } catch {
      return { view: "terrain", collection: "" };
    }
  }
  $effect(() => {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify({ view, collection }));
    } catch {
      /* ignore */
    }
  });

  const pal = $derived(map.palette);
  const sets = $derived(pal?.tilesets?.filter((t) => !!t) ?? []);
  const set = $derived(sets.find((s) => s.name === collection) ?? null);
  const collections = $derived([{ value: "", label: "All collections" }, ...sets.map((s) => ({ value: s.name, label: s.name }))]);
  const allBrushes = $derived((pal?.brushes ?? []).filter((b): b is PaletteBrush => !!b));
  const creatures = $derived((pal?.creatures ?? []).filter((c): c is CreatureType => !!c));

  // ----- search -----
  const q = $derived(query.trim().toLowerCase());
  const named = <T extends { name: string }>(list: T[]) => (q ? list.filter((x) => x.name.toLowerCase().includes(q)) : list);

  // ----- terrain and decor -----
  const terrainAll = $derived(set ? ((set.terrain ?? []) as PaletteBrush[]) : allBrushes.filter((b) => b.kind !== "doodad"));
  const decorAll = $derived(set ? ((set.doodads ?? []) as PaletteBrush[]) : allBrushes.filter((b) => b.kind === "doodad"));
  const terrain = $derived(named(terrainAll));
  const decor = $derived(named(decorAll));

  // ----- items -----
  const collectionItems = $derived.by<number[]>(() => {
    if (set) return [...new Set([...(set.items ?? []), ...(set.raw ?? [])])];
    return map.items?.ids ?? [];
  });
  // Names, ids and id ranges ("100-200") match, as in the item search.
  const baseItems = $derived.by<number[]>(() => {
    if (!q || !map.items) return collectionItems;
    const hits = map.items.search(q, Infinity);
    if (!set) return hits;
    const hit = new Set(hits);
    return collectionItems.filter((id) => hit.has(id));
  });
  const items = $derived.by<number[]>(() => {
    const ix = map.items;
    if (!ix || itemKind === "all") return baseItems;
    return baseItems.filter((id) => {
      const ground = ix.is(id, "ground");
      const edge = !ground && ix.top[id] > 0;
      switch (itemKind) {
        case "ground":
          return ground;
        case "edge":
          return edge;
        case "container":
          return ix.is(id, "container");
        case "pickup":
          return ix.is(id, "pickupable") && !ix.is(id, "container");
        default:
          return !ground && !edge && !ix.is(id, "pickupable") && !ix.is(id, "container");
      }
    });
  });

  // ----- creatures -----
  const matchedCreatures = $derived(named(creatures));
  const shownCreatures = $derived(matchedCreatures.filter((c) => who === "all" || (who === "npc") === !!c.npc));
  const separate = $derived(map.active?.props.spawnFormat === SpawnFormat.SpawnsSeparate);

  // Matches per tab while searching.
  const counts = $derived<Record<View, number>>({ terrain: terrain.length, decor: decor.length, items: baseItems.length, creatures: matchedCreatures.length });

  function brushSel(b: PaletteBrush): BrushSel {
    const kind = b.kind === "decoration" ? "wall" : b.kind;
    return { kind, name: b.name, look: b.look, label: b.name };
  }
  function itemSel(id: number): BrushSel {
    return { kind: "raw", item: id, look: id, label: map.items?.name(id) ?? `item ${id}` };
  }
  function creatureSel(c: CreatureType): BrushSel {
    return { kind: "creature", creature: c.name, npc: !!c.npc, label: c.name };
  }

  function editBrush(b: PaletteBrush) {
    map.editBrush = { name: b.name, type: b.kind };
    map.dialog = "brush";
  }

  async function removeBrush(b: PaletteBrush) {
    if (!(await ask({ title: "Remove brush", message: `Remove the brush "${b.name}"? Maps keep their items.`, ok: "Remove" }))) return;
    try {
      await MapService.RemoveBrush(b.name);
      await reloadPalette();
    } catch (e) {
      toast(errorMessage(e), "error");
    }
  }

  function brushMenu(e: MouseEvent, b: PaletteBrush) {
    openContextMenu(e, [
      { label: "Edit brush…", action: () => editBrush(b) },
      { label: "Remove brush", action: () => removeBrush(b) },
    ]);
  }

  function itemMenu(e: MouseEvent, id: number) {
    openContextMenu(e, [
      { label: "Find on the map", action: () => mapCommands.find({ ids: [id] }) },
      { label: "Replace on the map…", action: () => mapCommands.find({ ids: [id], replace: true }) },
      "-",
      { label: "Add to a collection…", action: () => addToCollection(id) },
      { label: "Copy id", action: () => navigator.clipboard.writeText(String(id)) },
    ]);
  }

  async function addToCollection(id: number) {
    const name = await askText({ title: "Add to a collection", label: "Collection", value: set && !set.auto ? set.name : "My items", ok: "Add" });
    if (!name) return;
    try {
      await MapService.AddToTileset(name, [id]);
      await reloadPalette();
      toast(`Added to ${name}.`, "success");
    } catch (e) {
      toast(errorMessage(e), "error");
    }
  }

  function newBrush() {
    map.editBrush = { name: "", type: view === "decor" ? "doodad" : "ground" };
    map.dialog = "brush";
  }

  const ITEM_CELL = 36;
</script>

{#snippet brushRow(b: PaletteBrush)}
  {@const sel = brushSel(b)}
  <button class="m-row" class:on={isBrushActive(sel)} title="{b.name} · double-click to edit" onclick={() => selectBrush(sel)} ondblclick={() => editBrush(b)} oncontextmenu={(e) => brushMenu(e, b)}>
    <span class="t-slot"><ItemIcon id={b.look} size={24} /></span>
    <span class="m-name">{b.name}</span>
  </button>
{/snippet}

<!-- Groups of tiles (stamps): shown whole in a grid of cards, an icon tells too little. -->
{#snippet stampCards(list: PaletteBrush[])}
  {#if list.length}
    <div class="m-sect">Groups<span class="grow"></span>{list.length}</div>
    <div class="stamps">
      {#each list as b (b.name)}
        {@const sel = brushSel(b)}
        <button class="stamp" class:on={isBrushActive(sel)} title="{b.name} · {b.preview?.w}×{b.preview?.h} tiles · double-click to edit" onclick={() => selectBrush(sel)} ondblclick={() => editBrush(b)} oncontextmenu={(e) => brushMenu(e, b)}>
          {#if b.preview}<BrushPreview preview={b.preview} />{/if}
          <span class="m-name">{b.name}</span>
        </button>
      {/each}
    </div>
  {/if}
{/snippet}

{#snippet itemCell(id: number)}
  {@const sel = itemSel(id)}
  <button class="m-cell" class:on={isBrushActive(sel)} title="{map.items?.name(id)} · {id}" onclick={() => selectBrush(sel)} oncontextmenu={(e) => itemMenu(e, id)}>
    <span class="t-slot"><ItemIcon {id} /></span>
  </button>
{/snippet}

{#snippet creatureRow(c: CreatureType)}
  {@const sel = creatureSel(c)}
  <button class="m-row" class:on={isBrushActive(sel)} onclick={() => selectBrush(sel)}>
    <span class="t-slot"><ItemIcon outfit={c.outfit.lookItem ? 0 : c.outfit.lookType} id={c.outfit.lookItem} size={24} /></span>
    <span class="m-name">{c.name}</span>
    {#if c.npc}<span class="m-meta">NPC</span>{/if}
  </button>
{/snippet}

{#snippet noMatch()}
  <div class="m-empty">
    <span>Nothing here matches “{query}”.</span>
    {#if set}<button class="t-btn" onclick={() => (collection = "")}>Search all collections</button>{/if}
  </div>
{/snippet}

{#snippet noBrushes()}
  <div class="m-empty">
    <span>No brushes yet. Brushes place borders, connect walls and scatter decoration for you.</span>
    <button class="t-btn primary" onclick={() => (map.dialog = "data")}>Import brushes…</button>
    <span class="small">or <button class="link" onclick={newBrush}>make one</button>; every item is in Items.</span>
  </div>
{/snippet}

<div class="lib">
  <SearchField bind:value={query} placeholder="Search by name or id" />

  <div class="t-tabs views" role="tablist">
    {#each VIEWS as v}
      <button class="t-tab" role="tab" class:on={view === v.id} aria-selected={view === v.id} title={v.title} onclick={() => (view = v.id)}>{v.label}{#if q}<span class="count" class:none={!counts[v.id]}>{counts[v.id]}</span>{/if}</button>
    {/each}
  </div>

  {#if view !== "creatures" && sets.length}
    <div class="bar" title="Collections are the tilesets of your brush data; the tabs below filter items by kind">
      <span class="t-label dim">Collection</span>
      <Select grow value={collection} options={collections} maxItems={16} onchange={(v) => (collection = v)} />
      {#if view !== "items"}<button class="t-icon-btn" title="New brush" onclick={newBrush}><Icon name="plus" /></button>{/if}
    </div>
  {/if}

  {#if view === "terrain"}
    <div class="m-body t-panel m-scroll">
      {#each TERRAIN_GROUPS as g}
        {@const list = terrain.filter((b) => g.kinds.includes(b.kind))}
        {#if list.length}
          <div class="m-sect">{g.label}</div>
          {#each list as b (b.name)}{@render brushRow(b)}{/each}
        {/if}
      {/each}
      {#if !terrain.length}
        {#if q}{@render noMatch()}{:else if allBrushes.length}<div class="m-empty">No terrain in this collection.</div>{:else}{@render noBrushes()}{/if}
      {:else if !q}
        {@const opt = { kind: "optionalBorder", label: "Optional border" }}
        <div class="m-sect">Tools</div>
        <button class="m-row" class:on={isBrushActive(opt)} title="Adds the optional border of a ground (like the gravel around mountains) where you paint" onclick={() => selectBrush(opt)}>
          <span class="m-swatch opt"></span><span class="m-name">Optional border</span>
        </button>
      {/if}
    </div>
  {:else if view === "decor"}
    <div class="m-body t-panel m-scroll">
      {#each decor.filter((b) => !b.preview) as b (b.name)}{@render brushRow(b)}{/each}
      {@render stampCards(decor.filter((b) => !!b.preview))}
      {#if !decor.length}
        {#if q}{@render noMatch()}{:else if allBrushes.length}<div class="m-empty">No decoration in this collection.</div>{:else}{@render noBrushes()}{/if}
      {/if}
    </div>
  {:else if view === "items"}
    <div class="t-tabs kinds" role="tablist">
      {#each ITEM_KINDS as k}
        <button class="t-tab" role="tab" class:on={itemKind === k.id} aria-selected={itemKind === k.id} title={k.title} onclick={() => (itemKind = k.id)}>{k.label}</button>
      {/each}
    </div>
    <div class="m-body t-panel grid-host">
      {#if items.length}
        <VirtualGrid count={items.length} cellWidth={ITEM_CELL} cellHeight={ITEM_CELL} gap={2}>
          {#snippet cell(i)}{@render itemCell(items[i])}{/snippet}
        </VirtualGrid>
      {:else if q}
        {@render noMatch()}
      {:else}
        <div class="m-empty">No items of this kind here.</div>
      {/if}
    </div>
  {:else}
    <div class="t-tabs kinds" role="tablist">
      <button class="t-tab" role="tab" class:on={who === "all"} aria-selected={who === "all"} onclick={() => (who = "all")}>All</button>
      <button class="t-tab" role="tab" class:on={who === "monster"} aria-selected={who === "monster"} onclick={() => (who = "monster")}>Monsters</button>
      <button class="t-tab" role="tab" class:on={who === "npc"} aria-selected={who === "npc"} onclick={() => (who = "npc")}>NPCs</button>
    </div>
    <div class="m-body t-panel m-scroll">
      {#if !q}
        <div class="m-sect">Spawn areas</div>
        <button class="m-row" class:on={isBrushActive({ kind: "spawn", label: "" })} title="Drag on the map: the brush size is the radius" onclick={() => selectBrush({ kind: "spawn", label: "Spawn area" })}>
          <span class="m-swatch spawn"></span><span class="m-name">{separate ? "Monster spawn area" : "Spawn area"}</span>
        </button>
        {#if separate}
          <button class="m-row" class:on={isBrushActive({ kind: "spawnNpc", label: "" })} onclick={() => selectBrush({ kind: "spawnNpc", label: "NPC spawn area" })}>
            <span class="m-swatch npc"></span><span class="m-name">NPC spawn area</span>
          </button>
        {/if}
      {/if}
      {#if shownCreatures.length}
        <div class="m-sect">{who === "npc" ? "NPCs" : who === "monster" ? "Monsters" : "Creatures"}<span class="grow"></span>{shownCreatures.length}</div>
        {#each shownCreatures as c (c.name)}{@render creatureRow(c)}{/each}
      {:else if q}
        {@render noMatch()}
      {:else}
        <div class="m-empty">
          <span>No creatures yet. They are read from the monster and NPC files of your server.</span>
          <button class="t-btn" onclick={() => (map.dialog = "data")}>Choose the server folder…</button>
        </div>
      {/if}
    </div>
  {/if}

  {#if map.recentBrushes.length}
    <div class="recent" title="Recently used: keys 1-9">
      {#each map.recentBrushes.slice(0, 9) as b, i}
        <button class="m-cell small" class:on={isBrushActive(b)} title="{i + 1}: {b.label}" onclick={() => selectBrush(b)}>
          <span class="t-slot"><ItemIcon id={b.look ?? b.item ?? 0} outfit={b.kind === "creature" ? creatureOutfit(b.creature) : 0} /></span>
          <span class="key">{i + 1}</span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .lib {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .stamps {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
    gap: 4px;
    padding: 2px;
  }
  .stamp {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-end;
    gap: 3px;
    min-width: 0;
    padding: 4px;
    background: rgba(0, 0, 0, 0.2);
    border: 1px solid transparent;
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  .stamp:hover {
    background: rgba(255, 255, 255, 0.06);
  }
  .stamp.on {
    background: var(--select);
    border-color: #b6b6b6;
    color: #fff;
  }
  .stamp .m-name {
    flex: none;
    max-width: 100%;
    font-size: 12px;
    text-align: center;
  }
  .count {
    margin-left: 4px;
    color: var(--gold);
    font: var(--fs-small) var(--font-small);
  }
  .count.none {
    color: var(--text-dim);
  }
  .bar {
    display: flex;
    gap: 4px;
    align-items: center;
  }
  .kinds > button,
  .views > button {
    font-size: 12px;
    padding: 0 2px;
  }
  .dim {
    color: var(--text-dim);
    font-size: 12px;
  }
  .grid-host {
    display: flex;
  }
  .recent {
    display: flex;
    gap: 1px;
    flex: none;
    overflow: hidden;
  }
  .recent .small {
    width: 30px;
    height: 34px;
  }
  .key {
    position: absolute;
    right: 1px;
    bottom: 0;
    font: var(--fs-small) / 10px var(--font-small);
    color: var(--text-dim);
    text-shadow: 1px 1px 0 #000;
  }
  .small {
    font-size: 12px;
  }
  .link {
    background: none;
    border: 0;
    padding: 0;
    color: var(--gold);
    font: inherit;
    cursor: pointer;
    text-decoration: underline;
  }
  .opt {
    background: #a87;
  }
  .spawn {
    background: rgba(255, 90, 90, 0.35);
    border-color: #f66;
  }
  .npc {
    background: rgba(90, 160, 255, 0.35);
    border-color: #6af;
  }
</style>
