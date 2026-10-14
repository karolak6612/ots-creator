package mapedit

import (
	"math"

	"github.com/nekiro/ots-creator/internal/materials"
	"github.com/nekiro/ots-creator/internal/otmap"
)

// DoodadTile is one tile of a doodad placement, relative to the cursor.
type DoodadTile struct {
	X     int          `json:"x"`
	Y     int          `json:"y"`
	Z     int          `json:"z"`
	Items []otmap.Item `json:"items"`
	// Replace clears the tile first: a tile of a group that brings its own
	// ground (a stamp made from a selection) replaces what is there.
	Replace bool `json:"replace,omitempty"`
}

// DoodadBuffer is the next placement of a doodad brush. It is generated
// before the click so the editor can preview exactly what will be placed.
type DoodadBuffer struct {
	Brush     string       `json:"brush"`
	Variation int          `json:"variation"`
	Size      int          `json:"size"`
	Shape     string       `json:"shape"`
	Tiles     []DoodadTile `json:"tiles"`
}

func itemFromSpec(s materials.ItemSpec) otmap.Item {
	it := otmap.Item{ID: s.ID, Subtype: s.Subtype}
	if s.ActionID != 0 || s.UniqueID != 0 || s.Text != "" {
		it.Ext = &otmap.ItemExt{ActionID: s.ActionID, UniqueID: s.UniqueID, Text: s.Text}
	}
	return it
}

func (a *doodadAlt) pickSingle(e *Editor) (otmap.Item, bool) {
	if len(a.singles) == 0 {
		return otmap.Item{}, false
	}
	if a.singleTotal <= 0 {
		return itemFromSpec(a.singles[0].item), true
	}
	roll := e.rng.IntN(a.singleTotal) + 1
	for _, s := range a.singles {
		if roll <= s.chance {
			return itemFromSpec(s.item), true
		}
		roll -= s.chance
	}
	return itemFromSpec(a.singles[0].item), true
}

func (a *doodadAlt) pickComposite(e *Editor) []materials.CompositeTile {
	if len(a.composites) == 0 {
		return nil
	}
	if a.compositeTotal <= 0 {
		return a.composites[0].tiles
	}
	roll := e.rng.IntN(a.compositeTotal) + 1
	for _, c := range a.composites {
		if roll <= c.chance {
			return c.tiles
		}
	}
	return a.composites[0].tiles
}

// useComposite decides between a composite and a single object.
func (a *doodadAlt) useComposite(e *Editor) bool {
	if !a.hasComposite {
		return false
	}
	if !a.hasSingleObject {
		return true
	}
	total := a.compositeTotal + a.singleTotal
	if total <= 0 {
		return true
	}
	return e.rng.IntN(total) < a.compositeTotal
}

// GenerateDoodad builds a new placement for a doodad brush. Thickness sets
// how many objects a bigger brush scatters.
func (e *Editor) GenerateDoodad(name string, variation, size int, shape string, thickness float64) *DoodadBuffer {
	d := e.Brushes.Doodad(name)
	buf := &DoodadBuffer{Brush: name, Variation: variation, Size: size, Shape: shape}
	if d == nil {
		e.doodad = buf
		return buf
	}
	alt := d.alt(variation)
	if alt == nil {
		e.doodad = buf
		return buf
	}
	tiles := map[[3]int]*DoodadTile{}
	put := func(x, y, z int, it otmap.Item) {
		k := [3]int{x, y, z}
		dt := tiles[k]
		if dt == nil {
			dt = &DoodadTile{X: x, Y: y, Z: z}
			tiles[k] = dt
		}
		dt.Items = append(dt.Items, it)
	}
	placeAt := func(x, y int) bool {
		if alt.useComposite(e) {
			comp := alt.pickComposite(e)
			for _, ct := range comp {
				if _, taken := tiles[[3]int{x + ct.X, y + ct.Y, ct.Z}]; taken {
					return false
				}
			}
			for _, ct := range comp {
				for _, it := range ct.Items {
					put(x+ct.X, y+ct.Y, ct.Z, itemFromSpec(it))
					if e.isGround(it.ID) {
						tiles[[3]int{x + ct.X, y + ct.Y, ct.Z}].Replace = true
					}
				}
			}
			return len(comp) > 0
		}
		if _, taken := tiles[[3]int{x, y, 0}]; taken {
			return false
		}
		it, ok := alt.pickSingle(e)
		if ok {
			put(x, y, 0, it)
		}
		return ok
	}
	if size <= 0 || d.opts.OneSize {
		placeAt(0, 0)
	} else {
		area := 0
		if shape == "circle" {
			if size == 1 {
				area = 5
			} else {
				area = int(0.5 + float64(size*size)*math.Pi)
			}
		} else {
			area = (2*size+1)*(2*size+1) + 1
		}
		var objects int
		if thickness > 0 {
			objects = int(float64(area) * thickness)
		} else {
			objects = d.opts.Thickness * area / max(d.opts.ThicknessCeiling, 1)
		}
		count := max(1, objects+e.rng.IntN(max(objects, 0)+1))
		for n := 0; n < count; n++ {
			for retry := 0; retry < 5; retry++ {
				x := e.rng.IntN(2*size+1) - size
				y := e.rng.IntN(2*size+1) - size
				if shape == "circle" && math.Sqrt(float64(x*x+y*y)) >= float64(size)+0.005 {
					continue
				}
				if placeAt(x, y) {
					break
				}
			}
		}
	}
	for _, dt := range tiles {
		buf.Tiles = append(buf.Tiles, *dt)
	}
	e.doodad = buf
	return buf
}

// placeDoodad puts the current buffer at the cursor and generates the next
// one. alt places on blocking tiles and duplicates.
func (e *Editor) placeDoodad(tx *Tx, center otmap.Position, alt bool, thickness float64) {
	buf := e.doodad
	if buf == nil {
		return
	}
	d := e.Brushes.Doodad(buf.Brush)
	if d == nil {
		return
	}
	var reborder []otmap.Position
	for _, dt := range buf.Tiles {
		p, ok := center.Add(dt.X, dt.Y, dt.Z)
		if !ok {
			continue
		}
		cur := tx.Get(p)
		if !dt.Replace && !(d.opts.OnBlocking || alt) && (cur == nil || e.blocking(cur)) {
			continue
		}
		if cur != nil && !dt.Replace && !d.opts.OnDuplicate && !alt {
			dup := false
			for _, it := range cur.Items {
				if d.owns(it.ID) {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		}
		t := tx.Edit(p)
		if dt.Replace {
			// A stamp tile is copied as it was: its ground and items in their
			// order. Placing item by item would let ground equivalents of
			// borders and stack rules change it.
			t.Ground = otmap.Item{}
			t.Items = nil
			for i, it := range dt.Items {
				if i == 0 && e.isGround(it.ID) {
					t.Ground = it.Clone()
					continue
				}
				t.Items = append(t.Items, it.Clone())
			}
			if d.opts.RemoveOptionalBorder {
				t.State &^= otmap.StateOptionalBorder
			}
			continue
		}
		for _, it := range dt.Items {
			// Walls of the doodad replace walls already there.
			if w, _ := e.Brushes.wallOf(it.ID); w != nil {
				e.cleanWalls(t, nil)
			}
			if d.opts.RedoBorders && e.isGround(it.ID) {
				reborder = append(reborder, p)
			}
			e.addItem(t, it.Clone())
		}
		if d.opts.RemoveOptionalBorder {
			t.State &^= otmap.StateOptionalBorder
		}
	}
	if len(reborder) > 0 && e.Settings.Automagic {
		seen := map[otmap.Position]bool{}
		for _, p := range reborder {
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if q, ok := p.Add(dx, dy, 0); ok && !seen[q] {
						seen[q] = true
						e.borderize(tx, q)
						e.wallize(tx, q)
					}
				}
			}
		}
	}
	e.GenerateDoodad(buf.Brush, buf.Variation, buf.Size, buf.Shape, thickness)
}

// eraseDoodad removes doodad items from a tile: items of the brush only
// (DoodadEraseLike) or of any doodad brush.
func (e *Editor) eraseDoodad(t *otmap.Tile, d *DoodadBrush) {
	keep := t.Items[:0]
	for _, it := range t.Items {
		in := e.Brushes.info[it.ID]
		owned := in != nil && in.doodad != nil
		if owned && it.Complex() && e.Settings.EraserLeaveUnique {
			owned = false
		}
		if owned && e.Settings.DoodadEraseLike {
			owned = d.owns(it.ID)
		}
		if !owned {
			keep = append(keep, it)
		}
	}
	t.Items = keep
	if in := e.Brushes.info[t.Ground.ID]; t.Ground.ID != 0 && in != nil && in.doodad != nil {
		if !e.Settings.DoodadEraseLike || d.owns(t.Ground.ID) {
			t.Ground = otmap.Item{}
		}
	}
}

// Doodad returns the current doodad preview.
func (e *Editor) Doodad() *DoodadBuffer { return e.doodad }
