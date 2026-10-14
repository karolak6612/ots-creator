package mapedit

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nekiro/ots-creator/internal/otitems"
	"github.com/nekiro/ots-creator/internal/otmap"
)

// FindQuery searches items on the map.
type FindQuery struct {
	// IDs are server ids to find (empty: use Name or Kind).
	IDs []uint16 `json:"ids"`
	// Name matches item names (case-insensitive substring).
	Name string `json:"name"`
	// Kind is "unique", "action", "container", "writeable", "teleport",
	// "door", "duplicate" (same item twice on a tile) or "".
	Kind string `json:"kind"`
	// ActionID / UniqueID match exact ids (0 = any).
	ActionID uint16 `json:"actionId"`
	UniqueID uint16 `json:"uniqueId"`
	// InSelection limits the search to the selection.
	InSelection bool `json:"inSelection"`
	// Containers also searches container contents.
	Containers bool `json:"containers"`
	Limit      int  `json:"limit"`
}

// FindResult is one found item.
type FindResult struct {
	Pos  otmap.Position `json:"pos"`
	ID   uint16         `json:"id"`
	Name string         `json:"name"`
	// Detail is the matched attribute ("aid 1000").
	Detail string `json:"detail"`
}

// Find searches the map.
func (e *Editor) Find(q FindQuery) ([]FindResult, bool) {
	limit := q.Limit
	if limit <= 0 {
		limit = 5000
	}
	name := strings.ToLower(strings.TrimSpace(q.Name))
	match := func(it *otmap.Item) (bool, string) {
		typ := e.itemType(it.ID)
		switch {
		case len(q.IDs) > 0:
			if !slices.Contains(q.IDs, it.ID) {
				return false, ""
			}
		case name != "":
			if typ == nil || !strings.Contains(strings.ToLower(typ.Name), name) {
				return false, ""
			}
		}
		if q.ActionID != 0 && it.ActionID() != q.ActionID {
			return false, ""
		}
		if q.UniqueID != 0 && it.UniqueID() != q.UniqueID {
			return false, ""
		}
		detail := ""
		switch q.Kind {
		case "unique":
			if it.UniqueID() == 0 {
				return false, ""
			}
			detail = "uid " + strconv.Itoa(int(it.UniqueID()))
		case "action":
			if it.ActionID() == 0 {
				return false, ""
			}
			detail = "aid " + strconv.Itoa(int(it.ActionID()))
		case "container":
			if typ == nil || !typ.IsContainer() {
				return false, ""
			}
			n := 0
			if it.Ext != nil {
				n = len(it.Ext.Items)
			}
			detail = strconv.Itoa(n) + " items"
		case "writeable":
			if typ == nil || !(typ.Flags.Has(otitems.FlagWritable) || typ.Flags.Has(otitems.FlagReadable)) {
				return false, ""
			}
			if it.Ext != nil && it.Ext.Text != "" {
				detail = strconv.Quote(truncate(it.Ext.Text, 40))
			}
		case "teleport":
			if it.Ext == nil || it.Ext.Dest == nil {
				return false, ""
			}
			d := it.Ext.Dest
			detail = fmt.Sprintf("to %d, %d, %d", d.X, d.Y, d.Z)
		case "door":
			if typ == nil || !typ.IsDoor() {
				return false, ""
			}
			if it.Ext != nil && it.Ext.DoorID != 0 {
				detail = "door id " + strconv.Itoa(int(it.Ext.DoorID))
			}
		}
		if detail == "" && q.ActionID != 0 {
			detail = "aid " + strconv.Itoa(int(q.ActionID))
		}
		return true, detail
	}
	var out []FindResult
	more := false
	visit := func(p otmap.Position, t *otmap.Tile) {
		if more {
			return
		}
		if q.Kind == "duplicate" {
			seen := map[uint16]bool{}
			for _, it := range t.Items {
				if seen[it.ID] {
					out = append(out, FindResult{Pos: p, ID: it.ID, Name: e.name(it.ID), Detail: "duplicate"})
					break
				}
				seen[it.ID] = true
			}
			more = len(out) >= limit
			return
		}
		var walk func(items []otmap.Item)
		walk = func(items []otmap.Item) {
			for i := range items {
				if ok, d := match(&items[i]); ok {
					out = append(out, FindResult{Pos: p, ID: items[i].ID, Name: e.name(items[i].ID), Detail: d})
					if len(out) >= limit {
						more = true
						return
					}
				}
				if q.Containers && items[i].Ext != nil && len(items[i].Ext.Items) > 0 {
					walk(items[i].Ext.Items)
				}
			}
		}
		if t.Ground.ID != 0 {
			walk([]otmap.Item{t.Ground})
		}
		walk(t.Items)
	}
	if q.InSelection {
		for _, p := range e.Selected() {
			visit(p, e.Map.Tile(p))
		}
	} else {
		e.Map.Each(visit)
	}
	return out, more
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (e *Editor) name(id uint16) string {
	if t := e.itemType(id); t != nil {
		return t.DisplayName()
	}
	return "unknown item " + strconv.Itoa(int(id))
}

// eachTile visits the selection or the whole map.
func (e *Editor) eachTile(inSelection bool, fn func(p otmap.Position, t *otmap.Tile)) {
	if inSelection {
		for _, p := range e.Selected() {
			if t := e.Map.Tile(p); t != nil {
				fn(p, t)
			}
		}
		return
	}
	e.Map.Each(fn)
}

// mapItems rewrites items of tiles matching pred, recursing into
// containers. fn returns the new item list for one list.
func (e *Editor) rewriteItems(label string, inSelection bool, fn func(items []otmap.Item) ([]otmap.Item, bool)) int {
	tx := e.Begin(label, 0)
	count := 0
	var rec func(items []otmap.Item) ([]otmap.Item, bool)
	rec = func(items []otmap.Item) ([]otmap.Item, bool) {
		out, changed := fn(items)
		for i := range out {
			if out[i].Ext != nil && len(out[i].Ext.Items) > 0 {
				if sub, ch := rec(out[i].Ext.Items); ch {
					if !changed {
						out = slices.Clone(out)
						changed = true
					}
					out[i] = out[i].Clone()
					out[i].Ext.Items = sub
					out[i].Compact()
				}
			}
		}
		return out, changed
	}
	e.eachTile(inSelection, func(p otmap.Position, t *otmap.Tile) {
		items := append([]otmap.Item{t.Ground}, t.Items...)
		out, changed := rec(items)
		if !changed {
			return
		}
		count++
		nt := tx.Edit(p)
		nt.Ground = out[0]
		if nt.Ground.ID == 0 {
			nt.Ground = otmap.Item{}
		}
		nt.Items = slices.DeleteFunc(slices.Clone(out[1:]), func(it otmap.Item) bool { return it.ID == 0 })
	})
	tx.Commit()
	return count
}

// ReplaceItems replaces items with one id by another (to 0 removes them).
func (e *Editor) ReplaceItems(pairs map[uint16]uint16, inSelection bool) int {
	n := 0
	label := "Replace items"
	if len(pairs) == 1 {
		for _, to := range pairs {
			if to == 0 {
				label = "Remove items"
			}
		}
	}
	e.rewriteItems(label, inSelection, func(items []otmap.Item) ([]otmap.Item, bool) {
		var out []otmap.Item
		changed := false
		for i, it := range items {
			to, ok := pairs[it.ID]
			if !ok || it.ID == 0 {
				continue
			}
			if !changed {
				out = slices.Clone(items)
				changed = true
			}
			n++
			out[i].ID = to
		}
		if !changed {
			return items, false
		}
		return out, true
	})
	return n
}

// RemoveCorpses removes corpse items.
func (e *Editor) RemoveCorpses(inSelection bool) int {
	n := 0
	e.rewriteItems("Remove corpses", inSelection, func(items []otmap.Item) ([]otmap.Item, bool) {
		changed := false
		out := slices.Clone(items)
		for i, it := range items {
			if typ := e.itemType(it.ID); typ != nil && typ.Flags.Has(otitems.FlagCorpse) && !it.Complex() {
				out[i].ID = 0
				n++
				changed = true
			}
		}
		return out, changed
	})
	return n
}

// RemoveInvalid removes items the item database does not know.
func (e *Editor) RemoveInvalid(inSelection bool) int {
	n := 0
	e.rewriteItems("Remove invalid items", inSelection, func(items []otmap.Item) ([]otmap.Item, bool) {
		changed := false
		out := slices.Clone(items)
		for i, it := range items {
			if it.ID != 0 && e.itemType(it.ID) == nil {
				out[i].ID = 0
				n++
				changed = true
			}
		}
		return out, changed
	})
	return n
}

// RemoveUnreachable removes tiles no player can see: tiles farther than
// the client view (9x7 tiles) from any walkable tile within two floors.
func (e *Editor) RemoveUnreachable() int {
	walk := func(x, y, z int) bool {
		t := e.Map.At(x, y, z)
		return t != nil && !e.blocking(t)
	}
	var drop []otmap.Position
	e.Map.Each(func(p otmap.Position, _ *otmap.Tile) {
		x, y, z := int(p.X), int(p.Y), int(p.Z)
		for fz := max(z-2, 0); fz <= min(z+2, otmap.MaxFloor); fz++ {
			for dy := -7; dy <= 7; dy++ {
				for dx := -9; dx <= 9; dx++ {
					if walk(x+dx, y+dy, fz) {
						return
					}
				}
			}
		}
		drop = append(drop, p)
	})
	if len(drop) == 0 {
		return 0
	}
	tx := e.Begin("Remove unreachable tiles", 0)
	for _, p := range drop {
		t := tx.Edit(p)
		*t = otmap.Tile{}
	}
	return tx.Commit()
}

// RemoveEmptySpawns removes spawns without creatures in their area.
func (e *Editor) RemoveEmptySpawns() int {
	type sp struct {
		p   otmap.Position
		npc bool
	}
	var empty []sp
	e.Map.Each(func(p otmap.Position, t *otmap.Tile) {
		for _, npc := range []bool{false, true} {
			s := t.Spawn()
			if npc {
				s = t.SpawnNPC()
			}
			if s == nil {
				continue
			}
			found := false
			r := int(s.Radius)
			e.Map.EachIn(int(p.X)-r, int(p.Y)-r, int(p.X)+r, int(p.Y)+r, int(p.Z), int(p.Z), func(_ otmap.Position, ct *otmap.Tile) {
				if len(ct.Creatures()) > 0 {
					found = true
				}
			})
			if !found {
				empty = append(empty, sp{p, npc})
			}
		}
	})
	if len(empty) == 0 {
		return 0
	}
	tx := e.Begin("Remove empty spawns", 0)
	for _, s := range empty {
		t := tx.Edit(s.p)
		if s.npc {
			t.Ext.SpawnNPC = nil
		} else {
			t.Ext.Spawn = nil
		}
		t.Compact()
	}
	tx.Commit()
	return len(empty)
}

// ClearInvalidHouses clears house ids of tiles whose house does not exist.
func (e *Editor) ClearInvalidHouses() int {
	tx := e.Begin("Clear invalid houses", 0)
	n := 0
	e.Map.Each(func(p otmap.Position, t *otmap.Tile) {
		if t.HouseID != 0 && e.Map.Houses[t.HouseID] == nil {
			nt := tx.Edit(p)
			nt.HouseID = 0
			n++
		}
	})
	tx.Commit()
	return n
}

// ClearModified clears the modified state of all tiles (not undoable).
func (e *Editor) ClearModified() {
	e.Map.ClearModified()
	for _, c := range e.Map.Chunks() {
		e.dirty[otmap.MakeKey(c.X, c.Y, c.Z)] = true
	}
}

// BorderizeMap redoes all borders and walls.
func (e *Editor) BorderizeMap() int {
	tx := e.Begin("Borderize map", 0)
	var ps []otmap.Position
	e.Map.Each(func(p otmap.Position, _ *otmap.Tile) { ps = append(ps, p) })
	for _, p := range ps {
		e.borderize(tx, p)
		e.wallize(tx, p)
	}
	return tx.Commit()
}

// RandomizeMap rerolls all grounds.
func (e *Editor) RandomizeMap() int {
	tx := e.Begin("Randomize map", 0)
	var ps []otmap.Position
	e.Map.Each(func(p otmap.Position, _ *otmap.Tile) { ps = append(ps, p) })
	for _, p := range ps {
		e.randomize(tx, p)
	}
	return tx.Commit()
}

// Statistics summarizes the map.
type Statistics struct {
	Tiles          int   `json:"tiles"`
	Items          int   `json:"items"`
	Walkable       int   `json:"walkable"`
	Blocking       int   `json:"blocking"`
	PZ             int   `json:"pz"`
	HouseTiles     int   `json:"houseTiles"`
	Houses         int   `json:"houses"`
	Towns          int   `json:"towns"`
	Spawns         int   `json:"spawns"`
	Monsters       int   `json:"monsters"`
	NPCs           int   `json:"npcs"`
	Containers     int   `json:"containers"`
	ContainerItems int   `json:"containerItems"`
	ActionIDs      int   `json:"actionIds"`
	UniqueIDs      int   `json:"uniqueIds"`
	Teleports      int   `json:"teleports"`
	Waypoints      int   `json:"waypoints"`
	Floors         []int `json:"floors"`
	MinX           int   `json:"minX"`
	MinY           int   `json:"minY"`
	MaxX           int   `json:"maxX"`
	MaxY           int   `json:"maxY"`
}

// Stats computes map statistics.
func (e *Editor) Stats() Statistics {
	s := Statistics{Houses: len(e.Map.Houses), Towns: len(e.Map.Towns), Waypoints: len(e.Map.Waypoints), Floors: make([]int, otmap.MaxFloor+1)}
	s.MinX, s.MinY, s.MaxX, s.MaxY, _ = e.Map.Bounds()
	var count func(items []otmap.Item, nested bool)
	count = func(items []otmap.Item, nested bool) {
		for _, it := range items {
			if it.ID == 0 {
				continue
			}
			s.Items++
			if nested {
				s.ContainerItems++
			}
			if it.ActionID() != 0 {
				s.ActionIDs++
			}
			if it.UniqueID() != 0 {
				s.UniqueIDs++
			}
			if it.Ext != nil && it.Ext.Dest != nil {
				s.Teleports++
			}
			if typ := e.itemType(it.ID); typ != nil && typ.IsContainer() {
				s.Containers++
			}
			if it.Ext != nil {
				count(it.Ext.Items, true)
			}
		}
	}
	e.Map.Each(func(p otmap.Position, t *otmap.Tile) {
		s.Tiles++
		s.Floors[p.Z]++
		count([]otmap.Item{t.Ground}, false)
		count(t.Items, false)
		if e.blocking(t) {
			s.Blocking++
		} else {
			s.Walkable++
		}
		if t.Flags&otmap.FlagPZ != 0 {
			s.PZ++
		}
		if t.HouseID != 0 {
			s.HouseTiles++
		}
		if t.Spawn() != nil {
			s.Spawns++
		}
		if t.SpawnNPC() != nil {
			s.Spawns++
		}
		for _, c := range t.Creatures() {
			if c.NPC {
				s.NPCs++
			} else {
				s.Monsters++
			}
		}
	})
	return s
}

// ItemPatch changes attributes of an item. Nil fields stay unchanged.
type ItemPatch struct {
	ID          *uint16         `json:"id,omitempty"`
	Subtype     *uint16         `json:"subtype,omitempty"`
	ActionID    *uint16         `json:"actionId,omitempty"`
	UniqueID    *uint16         `json:"uniqueId,omitempty"`
	Text        *string         `json:"text,omitempty"`
	Description *string         `json:"description,omitempty"`
	Dest        *otmap.Position `json:"dest,omitempty"`
	ClearDest   bool            `json:"clearDest,omitempty"`
	DepotID     *uint16         `json:"depotId,omitempty"`
	DoorID      *uint8          `json:"doorId,omitempty"`
	Attrs       *[]otmap.Attr   `json:"attrs,omitempty"`
}

// ItemRef addresses an item: Index -1 is the ground, Path walks into
// container contents.
type ItemRef struct {
	Pos   otmap.Position `json:"pos"`
	Index int            `json:"index"`
	Path  []int          `json:"path"`
}

// ErrNoItem is returned for a reference to a missing item.
var ErrNoItem = errors.New("item not found")

// itemAt resolves a reference on a writable tile.
func itemAt(t *otmap.Tile, r ItemRef) (*otmap.Item, []otmap.Item, int, error) {
	var list []otmap.Item
	var it *otmap.Item
	if r.Index < 0 {
		if t.Ground.ID == 0 {
			return nil, nil, 0, ErrNoItem
		}
		it = &t.Ground
	} else {
		if r.Index >= len(t.Items) {
			return nil, nil, 0, ErrNoItem
		}
		list = t.Items
		it = &t.Items[r.Index]
	}
	idx := r.Index
	for _, i := range r.Path {
		if it.Ext == nil || i < 0 || i >= len(it.Ext.Items) {
			return nil, nil, 0, ErrNoItem
		}
		list = it.Ext.Items
		idx = i
		it = &it.Ext.Items[i]
	}
	return it, list, idx, nil
}

// UpdateItem applies a patch to an item.
func (e *Editor) UpdateItem(r ItemRef, p ItemPatch) error {
	if e.Map.Tile(r.Pos) == nil {
		return ErrNoItem
	}
	tx := e.Begin("Edit item", 0)
	defer tx.Commit()
	t := tx.Edit(r.Pos)
	it, _, _, err := itemAt(t, r)
	if err != nil {
		return err
	}
	if p.ID != nil {
		if e.itemType(*p.ID) == nil {
			return fmt.Errorf("unknown item %d", *p.ID)
		}
		it.ID = *p.ID
	}
	if p.Subtype != nil {
		it.Subtype = *p.Subtype
	}
	ext := it.Ensure()
	if p.ActionID != nil {
		ext.ActionID = *p.ActionID
	}
	if p.UniqueID != nil {
		ext.UniqueID = *p.UniqueID
	}
	if p.Text != nil {
		ext.Text = *p.Text
	}
	if p.Description != nil {
		ext.Description = *p.Description
	}
	if p.Dest != nil {
		d := *p.Dest
		ext.Dest = &d
	}
	if p.ClearDest {
		ext.Dest = nil
	}
	if p.DepotID != nil {
		ext.DepotID = *p.DepotID
	}
	if p.DoorID != nil {
		ext.DoorID = *p.DoorID
	}
	if p.Attrs != nil {
		ext.Attrs = slices.Clone(*p.Attrs)
	}
	it.Compact()
	return nil
}

// RemoveItem deletes an item (or container content).
func (e *Editor) RemoveItem(r ItemRef) error {
	if e.Map.Tile(r.Pos) == nil {
		return ErrNoItem
	}
	tx := e.Begin("Remove item", 0)
	defer tx.Commit()
	t := tx.Edit(r.Pos)
	if len(r.Path) == 0 {
		if r.Index < 0 {
			t.Ground = otmap.Item{}
			return nil
		}
		if r.Index >= len(t.Items) {
			return ErrNoItem
		}
		t.Items = slices.Delete(t.Items, r.Index, r.Index+1)
		return nil
	}
	parent := ItemRef{Pos: r.Pos, Index: r.Index, Path: r.Path[:len(r.Path)-1]}
	pit, _, _, err := itemAt(t, parent)
	if err != nil {
		return err
	}
	i := r.Path[len(r.Path)-1]
	if pit.Ext == nil || i >= len(pit.Ext.Items) {
		return ErrNoItem
	}
	pit.Ext.Items = slices.Delete(pit.Ext.Items, i, i+1)
	pit.Compact()
	return nil
}

// AddToContainer puts a new item into a container.
func (e *Editor) AddToContainer(r ItemRef, id uint16) error {
	if e.itemType(id) == nil {
		return fmt.Errorf("unknown item %d", id)
	}
	if e.Map.Tile(r.Pos) == nil {
		return ErrNoItem
	}
	tx := e.Begin("Add to container", 0)
	defer tx.Commit()
	t := tx.Edit(r.Pos)
	it, _, _, err := itemAt(t, r)
	if err != nil {
		return err
	}
	if typ := e.itemType(it.ID); typ == nil || !typ.IsContainer() {
		return errors.New("the item is not a container")
	}
	ext := it.Ensure()
	ext.Items = append(ext.Items, e.newItem(id))
	return nil
}

// MoveItem changes the stack position of an item on a tile.
func (e *Editor) MoveItem(p otmap.Position, from, to int) error {
	cur := e.Map.Tile(p)
	if cur == nil || from < 0 || from >= len(cur.Items) || to < 0 || to >= len(cur.Items) || from == to {
		return nil
	}
	tx := e.Begin("Reorder items", 0)
	defer tx.Commit()
	t := tx.Edit(p)
	it := t.Items[from]
	t.Items = slices.Delete(t.Items, from, from+1)
	t.Items = slices.Insert(t.Items, to, it)
	return nil
}

// AddItem puts a new item on a tile like a raw brush.
func (e *Editor) AddItem(p otmap.Position, id uint16) error {
	if e.itemType(id) == nil {
		return fmt.Errorf("unknown item %d", id)
	}
	tx := e.Begin("Add item", 0)
	defer tx.Commit()
	e.addItem(tx.Edit(p), e.newItem(id))
	return nil
}

// RotateItem turns an item into its "rotateTo" item.
func (e *Editor) RotateItem(r ItemRef) error {
	if e.Map.Tile(r.Pos) == nil {
		return ErrNoItem
	}
	tx := e.Begin("Rotate item", 0)
	defer tx.Commit()
	t := tx.Edit(r.Pos)
	it, _, _, err := itemAt(t, r)
	if err != nil {
		return err
	}
	typ := e.itemType(it.ID)
	if typ == nil || typ.RotateTo == 0 {
		return errors.New("the item cannot be rotated")
	}
	it.ID = typ.RotateTo
	return nil
}

// SwitchDoor opens or closes a door.
func (e *Editor) SwitchDoor(p otmap.Position, index int) error {
	cur := e.Map.Tile(p)
	if cur == nil || index < 0 || index >= len(cur.Items) {
		return ErrNoItem
	}
	tx := e.Begin("Switch door", 0)
	defer tx.Commit()
	if !e.switchDoor(tx.Edit(p), index) {
		return errors.New("no door brush knows this door")
	}
	return nil
}

// TilePatch changes tile properties.
type TilePatch struct {
	Flags   *uint32   `json:"flags,omitempty"`
	HouseID *uint32   `json:"houseId,omitempty"`
	Zones   *[]uint16 `json:"zones,omitempty"`
}

// UpdateTile applies a tile patch.
func (e *Editor) UpdateTile(p otmap.Position, patch TilePatch) {
	tx := e.Begin("Edit tile", 0)
	t := tx.Edit(p)
	if patch.Flags != nil {
		t.Flags = *patch.Flags
	}
	if patch.HouseID != nil {
		t.HouseID = *patch.HouseID
	}
	if patch.Zones != nil {
		t.EnsureExt().Zones = slices.Clone(*patch.Zones)
		t.Compact()
	}
	tx.Commit()
}

// CreaturePatch changes a creature (Remove deletes it).
type CreaturePatch struct {
	SpawnTime *int32  `json:"spawnTime,omitempty"`
	Direction *uint8  `json:"direction,omitempty"`
	Weight    *uint8  `json:"weight,omitempty"`
	Name      *string `json:"name,omitempty"`
	Remove    bool    `json:"remove,omitempty"`
}

// UpdateCreature edits a creature of a tile.
func (e *Editor) UpdateCreature(p otmap.Position, index int, patch CreaturePatch) error {
	cur := e.Map.Tile(p)
	if cur == nil || index < 0 || index >= len(cur.Creatures()) {
		return errors.New("creature not found")
	}
	tx := e.Begin("Edit creature", 0)
	defer tx.Commit()
	t := tx.Edit(p)
	c := &t.Ext.Creatures[index]
	if patch.Remove {
		t.Ext.Creatures = slices.Delete(t.Ext.Creatures, index, index+1)
		t.Compact()
		return nil
	}
	if patch.SpawnTime != nil {
		c.SpawnTime = *patch.SpawnTime
	}
	if patch.Direction != nil {
		c.Direction = *patch.Direction % 4
	}
	if patch.Weight != nil {
		c.Weight = *patch.Weight
	}
	if patch.Name != nil && *patch.Name != "" {
		c.Name = *patch.Name
	}
	return nil
}

// UpdateSpawn sets the radius of a spawn (0 removes it).
func (e *Editor) UpdateSpawn(p otmap.Position, npc bool, radius int32) error {
	cur := e.Map.Tile(p)
	if cur == nil || (npc && cur.SpawnNPC() == nil) || (!npc && cur.Spawn() == nil) {
		return errors.New("spawn not found")
	}
	tx := e.Begin("Edit spawn", 0)
	defer tx.Commit()
	t := tx.Edit(p)
	s := &t.Ext.Spawn
	if npc {
		s = &t.Ext.SpawnNPC
	}
	if radius <= 0 {
		*s = nil
		t.Compact()
		return nil
	}
	(*s).Radius = min(radius, maxSpawnRadius)
	return nil
}
