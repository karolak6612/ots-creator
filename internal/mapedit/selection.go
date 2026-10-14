package mapedit

import (
	"cmp"
	"errors"
	"slices"

	"github.com/nekiro/ots-creator/internal/otmap"
)

// Floor ranges of a selection.
const (
	FloorsCurrent = "current"
	FloorsLower   = "lower"
	FloorsVisible = "visible"
)

// floorRange returns the floors a selection mode covers from floor z.
func floorRange(z int, mode string) (int, int) {
	switch mode {
	case FloorsLower:
		if z <= otmap.GroundFloor {
			return z, otmap.GroundFloor
		}
		return z, min(otmap.MaxFloor, z+2)
	case FloorsVisible:
		if z <= otmap.GroundFloor {
			return 0, otmap.GroundFloor
		}
		return z, min(otmap.MaxFloor, z+2)
	}
	return z, z
}

// selection tracking: positions of tiles with something selected.
func (e *Editor) syncSel(p otmap.Position) {
	if e.sel == nil {
		e.sel = map[otmap.Position]struct{}{}
	}
	if e.Map.Tile(p).Selected() {
		e.sel[p] = struct{}{}
	} else {
		delete(e.sel, p)
	}
}

// RebuildSelection scans the map for selected tiles (after loading).
func (e *Editor) RebuildSelection() {
	e.sel = map[otmap.Position]struct{}{}
	for _, c := range e.Map.Chunks() {
		c.Each(func(p otmap.Position, t *otmap.Tile) {
			if t.Selected() {
				e.sel[p] = struct{}{}
			}
		})
	}
}

// Selected returns the positions with something selected, sorted.
func (e *Editor) Selected() []otmap.Position {
	out := make([]otmap.Position, 0, len(e.sel))
	for p := range e.sel {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b otmap.Position) int {
		return cmp.Or(cmp.Compare(a.Z, b.Z), cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	return out
}

// SelectionCount returns the number of tiles with a selection.
func (e *Editor) SelectionCount() int { return len(e.sel) }

// SelectionBounds returns the selected area.
func (e *Editor) SelectionBounds() (min, max otmap.Position, ok bool) {
	first := true
	for p := range e.sel {
		if first {
			min, max, first = p, p, false
			continue
		}
		min.X, min.Y, min.Z = minU(min.X, p.X), minU(min.Y, p.Y), minU(min.Z, p.Z)
		max.X, max.Y, max.Z = maxU(max.X, p.X), maxU(max.Y, p.Y), maxU(max.Z, p.Z)
	}
	return min, max, !first
}

func minU[T uint8 | uint16](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func maxU[T uint8 | uint16](a, b T) T {
	if a > b {
		return a
	}
	return b
}

// ClearSelection deselects everything.
func (e *Editor) ClearSelection() {
	if len(e.sel) == 0 {
		return
	}
	tx := e.BeginSelection()
	for _, p := range e.Selected() {
		tx.Edit(p).SetSelected(false)
	}
	tx.Commit()
}

// SelectRect selects the tiles of a rectangle. add keeps the selection,
// remove deselects instead. compensate shifts lower floors like they are
// drawn (one tile right and down per floor).
func (e *Editor) SelectRect(r Rect, floors string, add, remove, compensate bool) {
	tx := e.BeginSelection()
	if !add && !remove {
		for _, p := range e.Selected() {
			tx.Edit(p).SetSelected(false)
		}
	}
	x0, y0, x1, y1 := r.bounds()
	z := int(r.From.Z)
	zs, ze := floorRange(z, floors)
	for fz := zs; fz <= ze; fz++ {
		off := 0
		if compensate {
			off = fz - z
		}
		e.Map.EachIn(x0+off, y0+off, x1+off, y1+off, fz, fz, func(p otmap.Position, t *otmap.Tile) {
			if t.Selected() == !remove && (remove || allSelected(t)) {
				return
			}
			tx.Edit(p).SetSelected(!remove)
		})
	}
	tx.Commit()
}

func allSelected(t *otmap.Tile) bool {
	c := t.Clone()
	c.SetSelected(true)
	return equalSelection(c, t)
}

func equalSelection(a, b *otmap.Tile) bool {
	if a.Ground.Selected != b.Ground.Selected || len(a.Items) != len(b.Items) {
		return false
	}
	for i := range a.Items {
		if a.Items[i].Selected != b.Items[i].Selected {
			return false
		}
	}
	return true
}

// SelectAt selects the top thing of a tile: a creature, the spawn, or the
// top item. toggle adds or removes it from the selection instead of
// replacing it. It returns what was selected ("creature", "spawn", "item"
// or "").
func (e *Editor) SelectAt(p otmap.Position, toggle bool, showCreatures, showSpawns bool) string {
	t := e.Map.Tile(p)
	tx := e.BeginSelection()
	defer tx.Commit()
	if !toggle {
		for _, q := range e.Selected() {
			if q != p {
				tx.Edit(q).SetSelected(false)
			}
		}
	}
	if t == nil {
		return ""
	}
	nt := tx.Edit(p)
	if !toggle {
		nt.SetSelected(false)
	}
	if showCreatures && len(nt.Creatures()) > 0 {
		c := &nt.Ext.Creatures[len(nt.Ext.Creatures)-1]
		c.Selected = !toggle || !c.Selected
		return "creature"
	}
	if showSpawns && nt.Spawn() != nil {
		nt.Ext.Spawn.Selected = !toggle || !nt.Ext.Spawn.Selected
		return "spawn"
	}
	if showSpawns && nt.SpawnNPC() != nil {
		nt.Ext.SpawnNPC.Selected = !toggle || !nt.Ext.SpawnNPC.Selected
		return "spawn"
	}
	if n := len(nt.Items); n > 0 {
		nt.Items[n-1].Selected = !toggle || !nt.Items[n-1].Selected
		return "item"
	}
	if nt.Ground.ID != 0 {
		nt.Ground.Selected = !toggle || !nt.Ground.Selected
		return "item"
	}
	return ""
}

// SelectItem selects one item of a tile (index -1 is the ground).
func (e *Editor) SelectItem(p otmap.Position, index int, add bool) {
	tx := e.BeginSelection()
	defer tx.Commit()
	if !add {
		for _, q := range e.Selected() {
			tx.Edit(q).SetSelected(false)
		}
	}
	t := e.Map.Tile(p)
	if t == nil {
		return
	}
	nt := tx.Edit(p)
	if index < 0 {
		nt.Ground.Selected = nt.Ground.ID != 0
	} else if index < len(nt.Items) {
		nt.Items[index].Selected = true
	}
}

// SelectTiles selects whole tiles (find results).
func (e *Editor) SelectTiles(ps []otmap.Position, add bool) {
	tx := e.BeginSelection()
	if !add {
		for _, q := range e.Selected() {
			tx.Edit(q).SetSelected(false)
		}
	}
	for _, p := range ps {
		if e.Map.Tile(p) != nil {
			tx.Edit(p).SetSelected(true)
		}
	}
	tx.Commit()
}

// ClipTile is one tile of the clipboard: what was selected on it.
type ClipTile struct {
	DX        int              `json:"dx"`
	DY        int              `json:"dy"`
	DZ        int              `json:"dz"`
	Ground    *otmap.Item      `json:"ground,omitempty"`
	Items     []otmap.Item     `json:"items,omitempty"`
	Creatures []otmap.Creature `json:"creatures,omitempty"`
	Spawn     *otmap.Spawn     `json:"spawn,omitempty"`
	SpawnNPC  *otmap.Spawn     `json:"spawnNpc,omitempty"`
	HouseID   uint32           `json:"houseId,omitempty"`
	Flags     uint32           `json:"flags,omitempty"`
}

// Clip is copied map content.
type Clip struct {
	Tiles  []ClipTile `json:"tiles"`
	Width  int        `json:"width"`
	Height int        `json:"height"`
}

// extract returns the selected parts of a tile; take also removes them.
func extract(t *otmap.Tile, take bool) ClipTile {
	var c ClipTile
	if t.Ground.ID != 0 && t.Ground.Selected {
		g := t.Ground.Clone()
		g.Selected = false
		c.Ground = &g
		if take {
			t.Ground = otmap.Item{}
		}
	}
	keep := t.Items[:0:0]
	for _, it := range t.Items {
		if it.Selected {
			cp := it.Clone()
			cp.Selected = false
			c.Items = append(c.Items, cp)
			if take {
				continue
			}
		}
		keep = append(keep, it)
	}
	if take {
		t.Items = keep
	}
	if t.Ext != nil {
		var cs []otmap.Creature
		for _, cr := range t.Ext.Creatures {
			if cr.Selected {
				cr.Selected = false
				c.Creatures = append(c.Creatures, cr)
				if take {
					continue
				}
			}
			cs = append(cs, cr)
		}
		if take {
			t.Ext.Creatures = cs
		}
		if s := t.Ext.Spawn; s != nil && s.Selected {
			cp := *s
			cp.Selected = false
			c.Spawn = &cp
			if take {
				t.Ext.Spawn = nil
			}
		}
		if s := t.Ext.SpawnNPC; s != nil && s.Selected {
			cp := *s
			cp.Selected = false
			c.SpawnNPC = &cp
			if take {
				t.Ext.SpawnNPC = nil
			}
		}
		if take {
			t.Compact()
		}
	}
	// A whole tile selection carries the house and flags too.
	if c.Ground != nil {
		c.HouseID, c.Flags = t.HouseID, t.Flags
	}
	return c
}

// put merges clipboard parts into a tile, selecting them when sel is set.
func (e *Editor) put(t *otmap.Tile, c ClipTile, sel bool) {
	if c.Ground != nil {
		g := c.Ground.Clone()
		g.Selected = sel
		t.Ground = g
		if c.HouseID != 0 || c.Flags != 0 {
			t.HouseID, t.Flags = c.HouseID, c.Flags
		}
	}
	for _, it := range c.Items {
		cp := it.Clone()
		cp.Selected = sel
		e.addItem(t, cp)
	}
	for _, cr := range c.Creatures {
		cr.Selected = sel
		t.EnsureExt().Creatures = append(t.EnsureExt().Creatures, cr)
	}
	if c.Spawn != nil {
		s := *c.Spawn
		s.Selected = sel
		t.EnsureExt().Spawn = &s
	}
	if c.SpawnNPC != nil {
		s := *c.SpawnNPC
		s.Selected = sel
		t.EnsureExt().SpawnNPC = &s
	}
}

func (c *ClipTile) empty() bool {
	return c.Ground == nil && len(c.Items) == 0 && len(c.Creatures) == 0 && c.Spawn == nil && c.SpawnNPC == nil
}

// Copy returns the selection relative to its top-left corner.
func (e *Editor) Copy() *Clip {
	min, max, ok := e.SelectionBounds()
	if !ok {
		return nil
	}
	clip := &Clip{Width: int(max.X-min.X) + 1, Height: int(max.Y-min.Y) + 1}
	for _, p := range e.Selected() {
		ct := extract(e.Map.Tile(p).Clone(), false)
		if ct.empty() {
			continue
		}
		ct.DX, ct.DY, ct.DZ = int(p.X-min.X), int(p.Y-min.Y), int(p.Z)-int(min.Z)
		clip.Tiles = append(clip.Tiles, ct)
	}
	return clip
}

// Cut copies the selection and deletes it.
func (e *Editor) Cut() *Clip {
	c := e.Copy()
	if c != nil {
		e.DeleteSelection("Cut")
	}
	return c
}

// Paste puts a clip with its top-left corner at p and selects it.
func (e *Editor) Paste(c *Clip, at otmap.Position) error {
	if c == nil || len(c.Tiles) == 0 {
		return errors.New("the clipboard is empty")
	}
	tx := e.Begin("Paste", 0)
	for _, q := range e.Selected() {
		tx.Edit(q).SetSelected(false)
	}
	changed := newPosSet()
	for _, ct := range c.Tiles {
		p, ok := at.Add(ct.DX, ct.DY, ct.DZ)
		if !ok {
			continue
		}
		e.put(tx.Edit(p), ct, true)
		if e.reshapes(ct) {
			changed.add(p)
		}
	}
	e.autoAround(tx, changed)
	tx.Commit()
	return nil
}

// reshapes reports whether placing or removing c changes the borders,
// walls, tables or carpets around it. Other items leave them alone, so
// moving a barrel does not redo hand-placed borders.
func (e *Editor) reshapes(c ClipTile) bool {
	if c.Ground != nil {
		return true
	}
	for _, it := range c.Items {
		if in := e.Brushes.info[it.ID]; in != nil && (in.wall != nil || in.table != nil || in.carpet != nil) {
			return true
		}
	}
	return false
}

// autoAround redoes borders and walls around changed tiles.
func (e *Editor) autoAround(tx *Tx, s *posSet) {
	if !e.Settings.Automagic || len(s.list) == 0 || len(s.list) > 100000 {
		return
	}
	for _, p := range s.ring().list {
		e.borderize(tx, p)
		e.wallize(tx, p)
		e.tableize(tx, p)
		e.carpetize(tx, p)
	}
}

// DeleteSelection removes everything selected.
func (e *Editor) DeleteSelection(label string) int {
	if len(e.sel) == 0 {
		return 0
	}
	if label == "" {
		label = "Delete"
	}
	tx := e.Begin(label, 0)
	changed := newPosSet()
	for _, p := range e.Selected() {
		if e.reshapes(extract(tx.Edit(p), true)) {
			changed.add(p)
		}
	}
	e.autoAround(tx, changed)
	return tx.Commit()
}

// MoveSelection moves the selection by an offset.
func (e *Editor) MoveSelection(dx, dy, dz int) error {
	if len(e.sel) == 0 || (dx == 0 && dy == 0 && dz == 0) {
		return nil
	}
	sel := e.Selected()
	for _, p := range sel {
		if _, ok := p.Add(dx, dy, dz); !ok {
			return errors.New("the selection would leave the map")
		}
	}
	tx := e.Begin("Move", 0)
	type moved struct {
		to otmap.Position
		c  ClipTile
	}
	var parts []moved
	changed := newPosSet()
	for _, p := range sel {
		c := extract(tx.Edit(p), true)
		if c.Ground != nil {
			// The house and flags move with the ground.
			t := tx.Edit(p)
			t.HouseID, t.Flags = 0, 0
		}
		to, _ := p.Add(dx, dy, dz)
		parts = append(parts, moved{to, c})
		if e.reshapes(c) {
			changed.add(p)
		}
	}
	for _, m := range parts {
		e.put(tx.Edit(m.to), m.c, true)
		if e.reshapes(m.c) {
			changed.add(m.to)
		}
	}
	e.autoAround(tx, changed)
	tx.Commit()
	return nil
}

// BorderizeSelection redoes borders of the selected tiles.
func (e *Editor) BorderizeSelection() int {
	tx := e.Begin("Borderize selection", 0)
	for _, p := range e.Selected() {
		e.reborder(tx, p, true)
		e.wallize(tx, p)
	}
	return tx.Commit()
}

// RandomizeSelection rerolls the grounds of the selected tiles.
func (e *Editor) RandomizeSelection() int {
	tx := e.Begin("Randomize selection", 0)
	for _, p := range e.Selected() {
		e.randomize(tx, p)
	}
	return tx.Commit()
}

func (e *Editor) randomize(tx *Tx, p otmap.Position) {
	cur := tx.Get(p)
	g := e.groundBrushOf(cur)
	if g == nil || !g.randomize {
		return
	}
	id := g.items.pick(e.rng)
	if id == 0 || id == cur.Ground.ID {
		return
	}
	t := tx.Edit(p)
	t.Ground.ID = id
}
