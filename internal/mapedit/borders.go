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

// borderize recomputes the ground borders of the tile at p.
func (e *Editor) borderize(tx *Tx, p otmap.Position) {
	cur := tx.Get(p)
	if cur == nil {
		// An empty tile can still get the outer border of a neighbor.
		if !e.anyGroundBrushAround(p) {
			return
		}
	}
	t := tx.Edit(p)
	self := e.groundBrushOf(t)
	x, y, z := int(p.X), int(p.Y), int(p.Z)
	type nb struct {
		done  bool
		brush *GroundBrush
	}
	var ns [8]nb
	for i, d := range neighbors8 {
		nx, ny := x+d[0], y+d[1]
		if !inRange(nx, ny) {
			continue
		}
		ns[i].brush = e.groundBrushOf(e.Map.At(nx, ny, z))
	}
	var clusters []borderCluster
	var specific []*borderBlock
	addSpecific := func(bb *borderBlock) {
		if len(bb.specific) > 0 && !slices.Contains(specific, bb) {
			specific = append(specific, bb)
		}
	}
	optional := e.hasOptionalBorder(t)
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
				t.State &^= otmap.StateOptionalBorder
			}
		}
		ns[i].done = true
	}
	sort.SliceStable(clusters, func(a, b int) bool { return clusters[a].z < clusters[b].z })
	e.cleanBorders(t)
	for i := len(clusters) - 1; i >= 0; i-- {
		c := clusters[i]
		if c.border == nil {
			continue
		}
		for _, dir := range borderPieces(c.align) {
			if id := c.border.tiles[dir]; id != 0 {
				addBorderItem(t, id)
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
					addBorderItem(t, c.border.tiles[side])
				}
			}
		}
	}
	for _, bb := range specific {
		for _, sc := range bb.specific {
			e.applySpecific(t, sc)
		}
	}
}

func (e *Editor) anyGroundBrushAround(p otmap.Position) bool {
	x, y, z := int(p.X), int(p.Y), int(p.Z)
	for _, d := range neighbors8 {
		if g := e.groundBrushOf(e.Map.At(x+d[0], y+d[1], z)); g != nil && g.hasOuterZilchBorder() {
			return true
		}
	}
	return false
}

// applySpecific runs a specific case of a border rule on the border items
// at the bottom of the stack.
func (e *Editor) applySpecific(t *otmap.Tile, sc specificCase) {
	if len(sc.match) == 0 {
		return
	}
	matches := 0
	for _, it := range t.Items {
		if !e.Brushes.isBorder(it.ID) {
			break
		}
		if sc.matchGroup > 0 {
			if in := e.Brushes.info[it.ID]; in != nil && in.borderGroup == sc.matchGroup && in.borderAlign == sc.groupAlign {
				matches++
				continue
			}
		}
		for _, id := range sc.match {
			if it.ID == id {
				matches++
			}
		}
	}
	if matches != len(sc.match) {
		return
	}
	if sc.deleteAll {
		out := t.Items[:0]
		bottom := true
		for _, it := range t.Items {
			if bottom && !e.Brushes.isBorder(it.ID) {
				bottom = false
			}
			if bottom && slices.Contains(sc.match, it.ID) {
				continue
			}
			out = append(out, it)
		}
		t.Items = out
		return
	}
	for i := range t.Items {
		if !e.Brushes.isBorder(t.Items[i].ID) {
			return
		}
		if t.Items[i].ID == sc.replace {
			t.Items[i].ID = sc.with
			return
		}
	}
}
