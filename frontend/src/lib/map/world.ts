// Towns, houses, waypoints and zones: actions shared by the world outline,
// the inspector and the command palette.
import { ask, askText } from "../confirm.svelte";
import { toast } from "../state.svelte";
import { MapService, type HouseInfo, type Position, type Town } from "./api";
import { goTo, mapCommands, spriteSize } from "./commands";
import { edit, map, selectBrush } from "./state.svelte";

/** Tile at the center of the view. */
export function viewCenter(): Position {
  const c = map.cam;
  const S = spriteSize();
  const a = c.floor <= 7 ? 7 - c.floor : 0;
  return { x: Math.max(0, Math.floor(c.x / S) + a), y: Math.max(0, Math.floor(c.y / S) + a), z: c.floor };
}

export function towns(): Town[] {
  return (map.active?.towns ?? []).filter((t): t is Town => !!t);
}

export function houses(): HouseInfo[] {
  return (map.active?.houses ?? []).filter((h): h is HouseInfo => !!h);
}

export function townName(id: number): string {
  return towns().find((t) => t.id === id)?.name ?? "No town";
}

/** Adds a town with its temple at the view center and shows it. */
export async function addTown(): Promise<void> {
  const name = await askText({ title: "New town", label: "Name", value: `Town ${towns().length + 1}`, ok: "Add" });
  if (!name) return;
  const ch = await edit((id) => MapService.SaveTown(id, { id: 0, name, temple: viewCenter() }));
  if (ch?.count) {
    mapCommands.focus({ kind: "town", id: ch.count });
    toast("The temple is at the center of the view. Move it from the inspector.");
  }
}

export async function saveTown(t: Town): Promise<void> {
  await edit((id) => MapService.SaveTown(id, t));
}

export async function removeTown(t: Town): Promise<void> {
  const used = houses().filter((h) => h.townId === t.id).length;
  const message = used ? `${t.name} has ${used} house${used === 1 ? "" : "s"}; they keep their tiles but lose their town.` : `Remove ${t.name}?`;
  if (!(await ask({ title: "Remove town", message, ok: "Remove" }))) return;
  await edit((id) => MapService.RemoveTown(id, t.id));
  map.focus = null;
}

/** Adds a house and starts painting its tiles. */
export async function addHouse(townId = 0): Promise<void> {
  const n = houses().length + 1;
  const ch = await edit((id) => MapService.SaveHouse(id, { id: 0, name: `House ${n}`, townId: townId || towns()[0]?.id || 0, rent: 0, guildhall: false, exit: { x: 0, y: 0, z: 0 } }));
  if (!ch?.count) return;
  const h = ch.info.houses?.find((x) => x?.id === ch.count);
  if (h) {
    mapCommands.focus({ kind: "house", id: h.id });
    paintHouse(h);
  }
}

export function paintHouse(h: HouseInfo): void {
  selectBrush({ kind: "house", house: h.id, label: h.name });
}

export async function saveHouse(h: HouseInfo): Promise<void> {
  await edit((id) => MapService.SaveHouse(id, { id: h.id, name: h.name, townId: h.townId, rent: h.rent, guildhall: h.guildhall, exit: h.exit, clientId: h.clientId, beds: h.beds }));
}

export async function removeHouse(h: HouseInfo): Promise<void> {
  if (!(await ask({ title: "Remove house", message: `Remove ${h.name}? Its tiles stop being house tiles.`, ok: "Remove" }))) return;
  await edit((id) => MapService.RemoveHouse(id, h.id));
  map.focus = null;
}

/** Centers the view on the exit of a house, or on its first tile. */
export async function goHouse(h: HouseInfo): Promise<void> {
  if (h.exit?.x) return goTo(h.exit);
  const tiles = await MapService.HouseTiles(map.activeId!, h.id);
  if (tiles?.length) goTo(tiles[0]);
  else toast("The house has no tiles yet: paint them on the map.");
}

export async function selectHouseTiles(h: HouseInfo): Promise<void> {
  const tiles = await MapService.HouseTiles(map.activeId!, h.id);
  if (!tiles?.length) return toast("The house has no tiles yet.");
  await edit((id) => MapService.SelectTiles(id, tiles, false));
  goTo(tiles[0]);
}

/** Asks for a name and lets the next click place the waypoint. */
export async function addWaypoint(): Promise<void> {
  const name = await askText({ title: "New waypoint", label: "Name", ok: "Place on the map" });
  if (!name) return;
  if (map.active?.waypoints?.some((w) => w?.name === name)) return toast(`There is already a waypoint "${name}".`, "error");
  selectBrush({ kind: "waypoint", waypoint: name, label: `Waypoint ${name}` });
  map.focus = { kind: "waypoint", name };
  toast(`Click the map to place "${name}".`);
}

export async function renameWaypoint(old: string, name: string): Promise<void> {
  const w = map.active?.waypoints?.find((x) => x?.name === old);
  if (!name || !w || name === old) return;
  const ch = await edit((id) => MapService.SaveWaypoint(id, old, { name, pos: w.pos }));
  if (ch && map.focus?.kind === "waypoint") map.focus = { kind: "waypoint", name };
}

export async function removeWaypoint(name: string): Promise<void> {
  await edit((id) => MapService.RemoveWaypoint(id, name));
  map.focus = null;
}

export async function addZone(): Promise<void> {
  const name = await askText({ title: "New zone", label: "Name", ok: "Add" });
  if (!name) return;
  const ch = await edit((id) => MapService.SaveZone(id, { id: 0, name }));
  if (ch?.count) {
    map.focus = { kind: "zone", id: ch.count };
    selectBrush({ kind: "zone", zone: ch.count, label: `Zone ${name}` });
  }
}

export async function removeZone(zid: number): Promise<void> {
  await edit((id) => MapService.RemoveZone(id, zid));
  map.focus = null;
}

/** Lets the next click on the map choose a position. */
export function pickPosition(label: string, done: (p: Position) => void): void {
  map.pick = { label, done };
}
