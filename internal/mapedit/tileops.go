package mapedit

import (
	"slices"

	"github.com/nekiro/ots-creator/internal/otitems"
	"github.com/nekiro/ots-creator/internal/otmap"
)

// topOrder returns the stack order of an item: 1 borders, 2 bottom items,
// 3 top items, 0 common items. Border items of materials count as 1.
func (e *Editor) topOrder(id uint16) uint8 {
	t := e.Items.Get(id)
	var o uint8
	if t != nil {
		o = t.TopOrder
	}
	if o == 0 && e.Brushes.isBorder(id) {
		o = 1
	}
	return o
}

func (e *Editor) isGround(id uint16) bool {
	t := e.Items.Get(id)
	return t != nil && t.IsGround()
}

func (e *Editor) itemType(id uint16) *otitems.Type { return e.Items.Get(id) }

// addItem puts an item on a tile like the server stacks it: ground replaces
// the ground, bottom items go below common items ordered by their top
// order, common items go on top.
func (e *Editor) addItem(t *otmap.Tile, it otmap.Item) {
	if e.isGround(it.ID) {
		t.Ground = it
		return
	}
	if in := e.Brushes.info[it.ID]; in != nil && in.groundEquivalent != 0 {
		t.Ground = otmap.Item{ID: in.groundEquivalent}
		t.Items = slices.Insert(t.Items, 0, it)
		return
	}
	order := e.topOrder(it.ID)
	if order == 0 {
		t.Items = append(t.Items, it)
		return
	}
	i := 0
	for ; i < len(t.Items); i++ {
		o := e.topOrder(t.Items[i].ID)
		if o == 0 || order < o {
			break
		}
	}
	t.Items = slices.Insert(t.Items, i, it)
}

// addBorderItem puts a border item at the bottom of the stack.
func addBorderItem(t *otmap.Tile, id uint16) {
	t.Items = slices.Insert(t.Items, 0, otmap.Item{ID: id})
}

// cleanBorders removes the border items at the bottom of the stack.
func (e *Editor) cleanBorders(t *otmap.Tile) {
	n := 0
	for n < len(t.Items) && e.Brushes.isBorder(t.Items[n].ID) {
		n++
	}
	if n > 0 {
		t.Items = slices.Delete(t.Items, 0, n)
	}
}

func (e *Editor) groundBrushOf(t *otmap.Tile) *GroundBrush {
	if t == nil || t.Ground.ID == 0 {
		return nil
	}
	return e.Brushes.GroundOf(t.Ground.ID)
}

func (e *Editor) hasOptionalBorder(t *otmap.Tile) bool {
	if t.State&otmap.StateOptionalBorder != 0 {
		return true
	}
	for _, it := range t.Items {
		if in := e.Brushes.info[it.ID]; in != nil && in.optionalBorder {
			return true
		}
	}
	return false
}

// IsBlocking reports whether a tile blocks walking.
func (e *Editor) IsBlocking(t *otmap.Tile) bool { return e.blocking(t) }

// blocking reports whether a tile blocks walking (no ground counts).
func (e *Editor) blocking(t *otmap.Tile) bool {
	if t == nil || t.Ground.ID == 0 {
		return true
	}
	if typ := e.itemType(t.Ground.ID); typ != nil && typ.Flags.Has(otitems.FlagUnpassable) {
		return true
	}
	for _, it := range t.Items {
		if typ := e.itemType(it.ID); typ != nil && typ.Flags.Has(otitems.FlagUnpassable) {
			return true
		}
	}
	return false
}

func (e *Editor) hasFlag(t *otmap.Tile, f otitems.Flags) bool {
	if t == nil {
		return false
	}
	if typ := e.itemType(t.Ground.ID); typ != nil && typ.Flags.Has(f) {
		return true
	}
	for _, it := range t.Items {
		if typ := e.itemType(it.ID); typ != nil && typ.Flags.Has(f) {
			return true
		}
	}
	return false
}

func (e *Editor) hasWall(t *otmap.Tile) bool {
	for _, it := range t.Items {
		if w, _ := e.Brushes.wallOf(it.ID); w != nil {
			return true
		}
	}
	return false
}

func (e *Editor) hasCarpet(t *otmap.Tile) bool {
	for _, it := range t.Items {
		if in := e.Brushes.info[it.ID]; in != nil && in.carpet != nil {
			return true
		}
	}
	return false
}

func (e *Editor) hasTable(t *otmap.Tile) bool {
	for _, it := range t.Items {
		if in := e.Brushes.info[it.ID]; in != nil && in.table != nil {
			return true
		}
	}
	return false
}

// pos is a shorthand for building positions from ints (callers check range).
func pos(x, y, z int) otmap.Position { return otmap.Position{X: uint16(x), Y: uint16(y), Z: uint8(z)} }

func inRange(x, y int) bool { return x >= 0 && y >= 0 && x <= 0xFFFF && y <= 0xFFFF }

// neighbors8 lists the offsets NW, N, NE, W, E, SW, S, SE (bit order of
// the border tables).
var neighbors8 = [8][2]int{{-1, -1}, {0, -1}, {1, -1}, {-1, 0}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}}

// newItem creates an item, giving stackables a count of one.
func (e *Editor) newItem(id uint16) otmap.Item {
	it := otmap.Item{ID: id}
	if t := e.itemType(id); t != nil && t.Flags.Has(otitems.FlagStackable) {
		it.Subtype = 1
	}
	return it
}
