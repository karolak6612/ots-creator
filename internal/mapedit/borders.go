package mapedit

import (
	"slices"
	"sort"

	"github.com/nekiro/ots-creator/internal/otmap"
)

// Neighbor bits, in the order of neighbors8.
const (
	bitNW = 1 << iota
	bitN
	bitNE
	bitW
	bitE
	bitSW
	bitS
	bitSE
)

// borderPieces returns the border pieces a tile gets from neighbors (bits
// of neighbors8) that hold the other brush. Two adjacent sides make a
// diagonal; other sides make straight pieces; a corner neighbor makes a
// corner piece when neither of its sides is set.
func borderPieces(mask int) []int {
	n, e, s, w := mask&bitN != 0, mask&bitE != 0, mask&bitS != 0, mask&bitW != 0
	sides := 0
	for _, b := range []bool{n, e, s, w} {
		if b {
			sides++
		}
	}
	var out []int
	switch {
	case sides == 2 && n && w:
		out = append(out, edgeDNW)
	case sides == 2 && n && e:
		out = append(out, edgeDNE)
	case sides == 2 && s && e:
		out = append(out, edgeDSE)
	case sides == 2 && s && w:
		out = append(out, edgeDSW)
	default:
		if n {
			out = append(out, edgeN)
		}
		if e {
			out = append(out, edgeE)
		}
		if s {
			out = append(out, edgeS)
		}
		if w {
			out = append(out, edgeW)
		}
	}
	if mask&bitNW != 0 && !n && !w {
		out = append(out, edgeCNW)
	}
	if mask&bitNE != 0 && !n && !e {
		out = append(out, edgeCNE)
	}
	if mask&bitSW != 0 && !s && !w {
		out = append(out, edgeCSW)
	}
	if mask&bitSE != 0 && !s && !e {
		out = append(out, edgeCSE)
	}
	return out
}

// brushTo finds the border placed on a tile of brush first next to a tile
// of brush second (either can be nil: an empty tile).
func brushTo(first, second *GroundBrush) *borderBlock {
	switch {
	case first != nil && second != nil:
		if first.zOrder < second.zOrder && second.hasOuterBorder() {
			if first.hasInner {
				for _, bb := range first.borders {
					if !bb.outer && (bb.to == second.id || bb.to == toAll) {
						return bb
					}
				}
			}
			for _, bb := range second.borders {
				if bb.outer && (bb.to == first.id || bb.to == toAll) {
					return bb
				}
			}
		} else if first.hasInner {
			for _, bb := range first.borders {
				if !bb.outer && (bb.to == second.id || bb.to == toAll) {
					return bb
				}
			}
		}
	case first != nil:
		if first.hasInnerZilch {
			for _, bb := range first.borders {
				if !bb.outer && bb.to == toNone {
					return bb
				}
			}
		}
	case second != nil:
		if second.hasOuterZilchBorder() {
			for _, bb := range second.borders {
				if bb.outer && bb.to == toNone {
					return bb
				}
			}
		}
	}
	return nil
}

type borderCluster struct {
	align  int
	z      int
	border *autoBorder
}

// borderPlan is the border stack automagic wants on a tile.
type borderPlan struct {
	ids []uint16 // bottom to top
	// managed holds every piece of the border sets the plan used, so a new
	// plan also replaces other pieces of the same sets.
	managed      map[uint16]bool
	dropOptional bool
}

// planBorders computes the borders of a tile of brush self at p, reading
// the neighbors through ground. optional tells whether the tile carries an
// optional (mountain) border.
func (e *Editor) planBorders(p otmap.Position, self *GroundBrush, optional bool, ground func(otmap.Position) *GroundBrush) borderPlan {
	plan := borderPlan{managed: map[uint16]bool{}}
	type nb struct {
		done  bool
		brush *GroundBrush
	}
	var ns [8]nb
	for i, d := range neighbors8 {
		if q, ok := p.Add(d[0], d[1], 0); ok {
			ns[i].brush = ground(q)
		}
	}
	var clusters []borderCluster
	var specific []*borderBlock
	addSpecific := func(bb *borderBlock) {
		if len(bb.specific) > 0 && !slices.Contains(specific, bb) {
			specific = append(specific, bb)
		}
	}
	for i := range ns {
		if ns[i].done {
			continue
		}
		other := ns[i].brush
		switch {
		case self != nil && other != nil:
			if other.id == self.id {
				continue
			}
			if !(other.hasOuterBorder() || self.hasInner) {
				break
			}
			onlyMountain := false
			if other.friendOf(self) || self.friendOf(other) {
				if other.optional == nil {
					continue
				}
				onlyMountain = true
			}
			mask := 0
			for j := i; j < 8; j++ {
				if !ns[j].done && ns[j].brush != nil && ns[j].brush.id == other.id {
					ns[j].done = true
					mask |= 1 << j
				}
			}
			if mask == 0 {
				break
			}
			if other.optional != nil && optional {
				clusters = append(clusters, borderCluster{mask, 1 << 30, other.optional})
				if other.soloOptional {
					onlyMountain = true
				}
			}
			if onlyMountain {
				break
			}
			bb := brushTo(self, other)
			if bb == nil {
				break
			}
			found := false
			for k := range clusters {
				if clusters[k].border == bb.border {
					clusters[k].align |= mask
					clusters[k].z = max(clusters[k].z, other.zOrder)
					found = true
					break
				}
			}
			if !found {
				clusters = append(clusters, borderCluster{mask, other.zOrder, bb.border})
			}
			addSpecific(bb)
		case self != nil && other == nil:
			if !self.hasInnerZilch {
				break
			}
			mask := 0
			for j := i; j < 8; j++ {
				if !ns[j].done && ns[j].brush == nil {
					ns[j].done = true
					mask |= 1 << j
				}
			}
			if mask == 0 {
				break
			}
			if bb := brushTo(self, nil); bb != nil {
				if bb.border != nil {
					clusters = append(clusters, borderCluster{mask, 5000, bb.border})
				}
				addSpecific(bb)
			}
			continue
		case self == nil && other != nil:
			if !other.hasOuterZilchBorder() {
				break
			}
			mask := 0
			for j := i; j < 8; j++ {
				if !ns[j].done && ns[j].brush != nil && ns[j].brush.id == other.id {
					ns[j].done = true
					mask |= 1 << j
				}
			}
			if mask == 0 {
				break
			}
			if bb := brushTo(nil, other); bb != nil {
				if bb.border != nil {
					clusters = append(clusters, borderCluster{mask, other.zOrder, bb.border})
				}
				addSpecific(bb)
			}
			if other.optional != nil && optional {
				clusters = append(clusters, borderCluster{mask, 1 << 30, other.optional})
			} else {
				plan.dropOptional = true
			}
		}
		ns[i].done = true
	}
	sort.SliceStable(clusters, func(a, b int) bool { return clusters[a].z < clusters[b].z })
	// Each piece goes to the bottom, so the lowest cluster ends lowest.
	put := func(id uint16) { plan.ids = slices.Insert(plan.ids, 0, id) }
	for i := len(clusters) - 1; i >= 0; i-- {
		c := clusters[i]
		if c.border == nil {
			continue
		}
		for _, id := range c.border.tiles {
			if id != 0 {
				plan.managed[id] = true
			}
		}
		for _, dir := range borderPieces(c.align) {
			if id := c.border.tiles[dir]; id != 0 {
				put(id)
				continue
			}
			// A missing diagonal is made of its two sides.
			var a, b int
			switch dir {
			case edgeDNW:
				a, b = edgeW, edgeN
			case edgeDNE:
				a, b = edgeE, edgeN
			case edgeDSW:
				a, b = edgeS, edgeW
			case edgeDSE:
				a, b = edgeS, edgeE
			}
			for _, side := range []int{a, b} {
				if side != 0 && c.border.tiles[side] != 0 {
					put(c.border.tiles[side])
				}
			}
		}
	}
	for _, bb := range specific {
		for _, sc := range bb.specific {
			plan.ids = e.applySpecific(plan.ids, sc)
			for _, id := range sc.match {
				plan.managed[id] = true
			}
			for _, id := range []uint16{sc.replace, sc.with} {
				if id != 0 {
					plan.managed[id] = true
				}
			}
		}
	}
	return plan
}

// applySpecific runs a specific case of a border rule on a border stack.
func (e *Editor) applySpecific(ids []uint16, sc specificCase) []uint16 {
	if len(sc.match) == 0 {
		return ids
	}
	matches := 0
	for _, id := range ids {
		if sc.matchGroup > 0 {
			if in := e.Brushes.info[id]; in != nil && in.borderGroup == sc.matchGroup && in.borderAlign == sc.groupAlign {
				matches++
				continue
			}
		}
		for _, m := range sc.match {
			if id == m {
				matches++
			}
		}
	}
	if matches != len(sc.match) {
		return ids
	}
	if sc.deleteAll {
		return slices.DeleteFunc(ids, func(id uint16) bool { return slices.Contains(sc.match, id) })
	}
	if i := slices.Index(ids, sc.replace); i >= 0 {
		ids[i] = sc.with
	}
	return ids
}

// borderize redoes the borders of the tile at p after its neighborhood
// changed. Unlike RME it does not wipe the tile: it compares the plans
// from before and after the action and swaps only the pieces of the
// border sets they use. Borders it does not know stay, and a tile whose
// plan did not change is left alone.
func (e *Editor) borderize(tx *Tx, p otmap.Position) { e.reborder(tx, p, false) }

// reborder redoes the borders at p. full replaces every material border
// on the tile; it is used by the borderize commands and when the ground
// of the tile itself changed brush.
func (e *Editor) reborder(tx *Tx, p otmap.Position, full bool) {
	cur := tx.Get(p)
	self := e.groundBrushOf(cur)
	now := e.planBorders(p, self, cur != nil && e.hasOptionalBorder(cur), func(q otmap.Position) *GroundBrush {
		return e.groundBrushOf(e.Map.Tile(q))
	})
	if cur == nil && len(now.ids) == 0 {
		return
	}
	prior := tx.Prior(p)
	var drop func(uint16) bool
	if full || e.groundBrushOf(prior) != self {
		drop = func(id uint16) bool {
			in := e.Brushes.info[id]
			return in != nil && in.border
		}
	} else {
		old := e.planBorders(p, self, prior != nil && e.hasOptionalBorder(prior), func(q otmap.Position) *GroundBrush {
			return e.groundBrushOf(tx.Prior(q))
		})
		if slices.Equal(old.ids, now.ids) && old.dropOptional == now.dropOptional {
			return
		}
		drop = func(id uint16) bool { return old.managed[id] || now.managed[id] || slices.Contains(old.ids, id) }
	}
	dropOptional := now.dropOptional && cur != nil && cur.State&otmap.StateOptionalBorder != 0
	if cur != nil && !dropOptional && slices.EqualFunc(e.restack(cur.Items, now.ids, drop), cur.Items, func(a, b otmap.Item) bool { return a.ID == b.ID }) {
		return
	}
	t := tx.Edit(p)
	t.Items = e.restack(t.Items, now.ids, drop)
	if dropOptional {
		t.State &^= otmap.StateOptionalBorder
	}
}

// restack returns items with the dropped border pieces at the bottom of
// the stack replaced by ids. Kept borders go right above the new ones.
// Pieces already there are reused, keeping their selection.
func (e *Editor) restack(items []otmap.Item, ids []uint16, drop func(uint16) bool) []otmap.Item {
	n := 0
	for n < len(items) && e.Brushes.isBorder(items[n].ID) {
		n++
	}
	var spare, kept []otmap.Item
	for _, it := range items[:n] {
		if drop(it.ID) {
			spare = append(spare, it)
		} else {
			kept = append(kept, it)
		}
	}
	out := make([]otmap.Item, 0, len(ids)+len(items))
	for _, id := range ids {
		it := otmap.Item{ID: id}
		if i := slices.IndexFunc(spare, func(s otmap.Item) bool { return s.ID == id }); i >= 0 {
			it = spare[i]
			spare = slices.Delete(spare, i, i+1)
		}
		out = append(out, it)
	}
	out = append(out, kept...)
	return append(out, items[n:]...)
}
