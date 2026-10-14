package mapedit

import (
	"slices"

	"github.com/nekiro/ots-creator/internal/otmap"
)

// Wall neighbor bits.
const (
	wallN = 1 << iota
	wallW
	wallE
	wallS
)

// wallShape returns the wall alignment for the neighbors that hold a
// matching wall. The full shape set is pole, ends, straight pieces,
// corners, T pieces and the intersection; their numbers equal the mask.
func wallShape(mask int, full bool) int {
	if full {
		return mask
	}
	// Reduced set (only straight pieces, the corner and the pole): only
	// the north and west neighbors decide.
	switch mask & (wallN | wallW) {
	case wallN:
		return wallVertical
	case wallW:
		return wallHorizontal
	case wallN | wallW:
		return wallNWDiagonal
	}
	return wallPole
}

// matchingWall reports whether the tile holds a wall that connects to w.
func (e *Editor) matchingWall(t *otmap.Tile, w *WallBrush) bool {
	if t == nil {
		return false
	}
	for _, it := range t.Items {
		wb, in := e.Brushes.wallOf(it.ID)
		if wb == nil || wb.decoration {
			continue
		}
		if wb == w || w.friendOf(wb) || wb.friendOf(w) {
			return !in.wallHate
		}
	}
	return false
}

// wallize reshapes the walls of the tile at p to match their neighbors.
func (e *Editor) wallize(tx *Tx, p otmap.Position) {
	cur := tx.Get(p)
	if cur == nil || !e.hasWall(cur) {
		return
	}
	t := tx.Edit(p)
	x, y, z := int(p.X), int(p.Y), int(p.Z)
	var out []otmap.Item
	items := t.Items
	for i := 0; i < len(items); i++ {
		it := items[i]
		wb, in := e.Brushes.wallOf(it.ID)
		if wb == nil {
			out = append(out, it)
			continue
		}
		if wb.decoration {
			// Decorations follow the wall below them (handled there).
			out = append(out, it)
			continue
		}
		mask := 0
		for bit, d := range map[int][2]int{wallN: {0, -1}, wallW: {-1, 0}, wallE: {1, 0}, wallS: {0, 1}} {
			if e.matchingWall(e.Map.At(x+d[0], y+d[1], z), wb) {
				mask |= bit
			}
		}
		if in.wallAlign == wallUntouchable {
			out = append(out, it)
			continue
		}
		newAlign := -1
		var newID uint16
		for _, full := range []bool{true, false} {
			shape := wallShape(mask, full)
			if in.wallAlign == shape {
				newAlign, newID = shape, it.ID
				break
			}
			if in.door {
				if id := wb.doorFor(shape, in.doorType, in.doorOpen); id != 0 {
					newAlign, newID = shape, id
					break
				}
				continue
			}
			if id := wb.pick(shape, e.rng); id != 0 {
				newAlign, newID = shape, id
				break
			}
		}
		if newAlign < 0 {
			out = append(out, it)
			continue
		}
		nw := it
		nw.ID = newID
		out = append(out, nw)
		// Decorations right above the wall take the same shape.
		for i+1 < len(items) {
			dec, din := e.Brushes.wallOf(items[i+1].ID)
			if dec == nil || !dec.decoration {
				break
			}
			i++
			d := items[i]
			if din.wallAlign != newAlign {
				if din.door {
					if id := dec.doorFor(newAlign, din.doorType, din.doorOpen); id != 0 {
						d.ID = id
					}
				} else if id := dec.pick(newAlign, e.rng); id != 0 {
					d.ID = id
				}
			}
			out = append(out, d)
		}
	}
	t.Items = out
}

// doorFor returns a door of a type and state for an alignment, falling back
// to the other state.
func (w *WallBrush) doorFor(align, doorType int, open bool) uint16 {
	var fallback uint16
	for b, n := w, 0; b != nil && n < 8; b, n = b.redirect, n+1 {
		for _, d := range b.nodes[align].doors {
			if d.doorType != doorType {
				continue
			}
			if d.open == open {
				return d.id
			}
			if fallback == 0 {
				fallback = d.id
			}
		}
	}
	return fallback
}

// cleanWalls removes the walls of a brush (all walls when w is nil).
func (e *Editor) cleanWalls(t *otmap.Tile, w *WallBrush) {
	t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool {
		wb, _ := e.Brushes.wallOf(it.ID)
		if wb == nil {
			return false
		}
		if w == nil {
			return true
		}
		_, in := e.Brushes.wallOf(it.ID)
		return w.hasWall(it.ID, in.wallAlign) || wb == w
	})
}

// drawWall places a wall of the brush; the alignment is fixed by wallize.
func (e *Editor) drawWall(t *otmap.Tile, w *WallBrush) {
	if w.decoration {
		e.drawDecoration(t, w)
		return
	}
	e.cleanWalls(t, w)
	for align := 0; align < wallAlignmentMax; align++ {
		if id := w.pick(align, e.rng); id != 0 {
			e.addItem(t, otmap.Item{ID: id})
			return
		}
	}
}

// cycleWall changes a wall of the brush to the next alignment (alt-click).
func (e *Editor) cycleWall(t *otmap.Tile, w *WallBrush) bool {
	for i := range t.Items {
		wb, in := e.Brushes.wallOf(t.Items[i].ID)
		if wb != w {
			continue
		}
		for k := 1; k < wallAlignmentMax; k++ {
			align := (in.wallAlign + k) % wallAlignmentMax
			if id := w.pick(align, e.rng); id != 0 {
				t.Items[i].ID = id
				return true
			}
		}
		return false
	}
	return false
}

// drawDecoration puts a decoration of the brush on every wall of the tile.
func (e *Editor) drawDecoration(t *otmap.Tile, d *WallBrush) {
	e.cleanWalls(t, d)
	var out []otmap.Item
	for _, it := range t.Items {
		out = append(out, it)
		wb, in := e.Brushes.wallOf(it.ID)
		if wb == nil || wb.decoration {
			continue
		}
		var id uint16
		if in.door {
			id = d.doorFor(in.wallAlign, in.doorType, in.doorOpen)
		} else {
			id = d.nodes[in.wallAlign].items.pick(e.rng)
		}
		if id != 0 {
			out = append(out, otmap.Item{ID: id})
		}
	}
	t.Items = out
}

// drawDoor turns the wall of the tile into a door of a type.
func (e *Editor) drawDoor(tx *Tx, p otmap.Position, doorType int, open bool) bool {
	cur := tx.Get(p)
	if cur == nil {
		return false
	}
	for i, it := range cur.Items {
		wb, in := e.Brushes.wallOf(it.ID)
		if wb == nil || wb.decoration {
			continue
		}
		state := open
		if in.door {
			state = in.doorOpen
		}
		id := wb.doorFor(in.wallAlign, doorType, state)
		if id == 0 {
			continue
		}
		t := tx.Edit(p)
		t.Items[i].ID = id
		// Decorations above follow.
		for j := i + 1; j < len(t.Items); j++ {
			dec, din := e.Brushes.wallOf(t.Items[j].ID)
			if dec == nil || !dec.decoration {
				break
			}
			if did := dec.doorFor(din.wallAlign, doorType, state); did != 0 {
				t.Items[j].ID = did
			}
		}
		if e.Settings.AutoDoorID && t.HouseID != 0 {
			e.assignDoorID(tx, t, i)
		}
		return true
	}
	return false
}

// removeDoor turns doors of the tile back into walls.
func (e *Editor) removeDoor(tx *Tx, p otmap.Position) bool {
	cur := tx.Get(p)
	if cur == nil {
		return false
	}
	for i, it := range cur.Items {
		wb, in := e.Brushes.wallOf(it.ID)
		if wb == nil || !in.door {
			continue
		}
		t := tx.Edit(p)
		if id := wb.pick(in.wallAlign, e.rng); id != 0 {
			t.Items[i].ID = id
			if t.Items[i].Ext != nil {
				t.Items[i].Ext.DoorID = 0
				t.Items[i].Compact()
			}
		}
		return true
	}
	return false
}

// SwitchDoor opens a closed door or closes an open one.
func (e *Editor) switchDoor(t *otmap.Tile, i int) bool {
	wb, in := e.Brushes.wallOf(t.Items[i].ID)
	if wb == nil || !in.door {
		return false
	}
	for _, d := range wb.nodes[in.wallAlign].doors {
		if d.doorType == in.doorType && d.open != in.doorOpen {
			t.Items[i].ID = d.id
			return true
		}
	}
	return false
}

// carpetPiece returns the carpet piece for neighbors holding the same
// carpet. Pieces are named by the sides the carpet continues to: three
// sides give the piece of the middle one, two adjacent sides a corner, all
// sides the center, or an inner diagonal when one corner is missing.
func carpetPiece(mask int) int {
	n, e, s, w := mask&bitN != 0, mask&bitE != 0, mask&bitS != 0, mask&bitW != 0
	switch {
	case n && e && s && w:
		missing := -1
		count := 0
		for _, c := range []struct {
			bit, piece int
		}{{bitNW, edgeDSE}, {bitNE, edgeDSW}, {bitSW, edgeDNE}, {bitSE, edgeDNW}} {
			if mask&c.bit == 0 {
				missing = c.piece
				count++
			}
		}
		if count == 1 {
			return missing
		}
		return edgeCenter
	case n && e && w && !s:
		return edgeN
	case e && s && w && !n:
		return edgeS
	case n && s && w && !e:
		return edgeW
	case n && e && s && !w:
		return edgeE
	case n && w && !e && !s:
		return edgeCNW
	case n && e && !w && !s:
		return edgeCNE
	case s && w && !n && !e:
		return edgeCSW
	case s && e && !n && !w:
		return edgeCSE
	}
	return edgeCenter
}

// tablePiece returns the table piece: horizontal tables win over vertical
// ones.
func tablePiece(mask int) int {
	n, e, s, w := mask&bitN != 0, mask&bitE != 0, mask&bitS != 0, mask&bitW != 0
	switch {
	case e && w:
		return tableHorizontal
	case e:
		return tableWest
	case w:
		return tableEast
	case n && s:
		return tableVertical
	case s:
		return tableNorth
	case n:
		return tableSouth
	}
	return tableAlone
}

func (e *Editor) neighborMask(p otmap.Position, has func(*otmap.Tile) bool) int {
	mask := 0
	x, y, z := int(p.X), int(p.Y), int(p.Z)
	for i, d := range neighbors8 {
		if has(e.Map.At(x+d[0], y+d[1], z)) {
			mask |= 1 << i
		}
	}
	return mask
}

func tileHas(t *otmap.Tile, fn func(id uint16) bool) bool {
	if t == nil {
		return false
	}
	for _, it := range t.Items {
		if fn(it.ID) {
			return true
		}
	}
	return false
}

// carpetize reshapes the carpets of the tile at p.
func (e *Editor) carpetize(tx *Tx, p otmap.Position) {
	cur := tx.Get(p)
	if cur == nil || !e.hasCarpet(cur) {
		return
	}
	t := tx.Edit(p)
	for i, it := range t.Items {
		in := e.Brushes.info[it.ID]
		if in == nil || in.carpet == nil {
			continue
		}
		c := in.carpet
		mask := e.neighborMask(p, func(nt *otmap.Tile) bool {
			return tileHas(nt, func(id uint16) bool { ni := e.Brushes.info[id]; return ni != nil && ni.carpet == c })
		})
		piece := carpetPiece(mask)
		if piece == in.carpetAlign {
			continue
		}
		if id := c.parts[piece].pick(e.rng); id != 0 {
			t.Items[i].ID = id
		} else if id := c.parts[edgeCenter].pick(e.rng); id != 0 {
			t.Items[i].ID = id
		}
	}
}

// tableize reshapes the tables of the tile at p.
func (e *Editor) tableize(tx *Tx, p otmap.Position) {
	cur := tx.Get(p)
	if cur == nil || !e.hasTable(cur) {
		return
	}
	t := tx.Edit(p)
	for i, it := range t.Items {
		in := e.Brushes.info[it.ID]
		if in == nil || in.table == nil {
			continue
		}
		tb := in.table
		mask := e.neighborMask(p, func(nt *otmap.Tile) bool {
			return tileHas(nt, func(id uint16) bool { ni := e.Brushes.info[id]; return ni != nil && ni.table == tb })
		})
		piece := tablePiece(mask)
		if piece == in.tableAlign {
			continue
		}
		if id := tb.parts[piece].pick(e.rng); id != 0 {
			t.Items[i].ID = id
		} else if id := tb.parts[tableAlone].pick(e.rng); id != 0 {
			t.Items[i].ID = id
		}
	}
}

func (e *Editor) drawCarpet(t *otmap.Tile, c *CarpetBrush) {
	e.removeCarpets(t)
	id := c.parts[edgeCenter].pick(e.rng)
	if id == 0 {
		for _, l := range c.parts {
			if id = l.pick(e.rng); id != 0 {
				break
			}
		}
	}
	if id != 0 {
		e.addItem(t, otmap.Item{ID: id})
	}
}

func (e *Editor) removeCarpets(t *otmap.Tile) {
	t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool {
		in := e.Brushes.info[it.ID]
		return in != nil && in.carpet != nil
	})
}

func (e *Editor) drawTable(t *otmap.Tile, tb *TableBrush) {
	e.removeTables(t)
	id := tb.parts[tableAlone].pick(e.rng)
	if id == 0 {
		for _, l := range tb.parts {
			if id = l.pick(e.rng); id != 0 {
				break
			}
		}
	}
	if id != 0 {
		e.addItem(t, otmap.Item{ID: id})
	}
}

func (e *Editor) removeTables(t *otmap.Tile) {
	t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool {
		in := e.Brushes.info[it.ID]
		return in != nil && in.table != nil
	})
}
