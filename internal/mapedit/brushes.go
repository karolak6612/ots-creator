// Package mapedit is the map editing core: brushes and their automatic
// borders, walls, carpets and tables, selection, clipboard, undo/redo and the
// map tools (find, replace, cleanup, statistics).
package mapedit

import (
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/nekiro/ots-creator/internal/materials"
	"github.com/nekiro/ots-creator/internal/otitems"
)

// Border alignments (also carpet pieces), RME numbering.
const (
	edgeNone = iota
	edgeN
	edgeE
	edgeS
	edgeW
	edgeCNW
	edgeCNE
	edgeCSW
	edgeCSE
	edgeDNW
	edgeDNE
	edgeDSE
	edgeDSW
	edgeCenter
	edgeCount
)

var edgeIndex = map[string]int{
	"n": edgeN, "e": edgeE, "s": edgeS, "w": edgeW, "cnw": edgeCNW, "cne": edgeCNE, "csw": edgeCSW, "cse": edgeCSE,
	"dnw": edgeDNW, "dne": edgeDNE, "dse": edgeDSE, "dsw": edgeDSW, "c": edgeCenter,
}

// Wall alignments, RME numbering (index into materials.WallAlignments).
const (
	wallPole         = 0
	wallHorizontal   = 6
	wallVertical     = 9
	wallNWDiagonal   = 3
	wallUntouchable  = 16
	wallAlignmentMax = 17
)

// Table alignments (index into materials.TableAlignments).
const (
	tableNorth = iota
	tableSouth
	tableEast
	tableWest
	tableHorizontal
	tableVertical
	tableAlone
	tableCount
)

// Door types (index into materials.DoorTypes plus one; 0 is undefined).
const doorUndefined = 0

// toAll and toNone are the special targets of border rules.
const (
	toNone = 0
	toAll  = -1
)

type chanceList struct {
	ids   []uint16
	cum   []int // cumulative chances
	total int
}

func newChanceList(cs []materials.Chance) chanceList {
	var l chanceList
	for _, c := range cs {
		if c.ID == 0 {
			continue
		}
		l.total += max(c.Chance, 0)
		l.ids = append(l.ids, c.ID)
		l.cum = append(l.cum, l.total)
	}
	return l
}

func (l *chanceList) pick(r *rand.Rand) uint16 {
	if len(l.ids) == 0 {
		return 0
	}
	if l.total <= 0 {
		return l.ids[0]
	}
	n := r.IntN(l.total) + 1
	for i, c := range l.cum {
		if n <= c {
			return l.ids[i]
		}
	}
	return l.ids[0]
}

func (l *chanceList) has(id uint16) bool { return slices.Contains(l.ids, id) }

// autoBorder is a runtime border: items by edge.
type autoBorder struct {
	id               uint32
	tiles            [edgeCount]uint16
	group            uint16
	groundEquivalent uint16
}

type specificCase struct {
	match      []uint16 // item ids to match (group conditions use group)
	matchGroup uint16
	groupAlign int
	replace    uint16
	with       uint16
	deleteAll  bool
}

type borderBlock struct {
	outer    bool
	to       int // brush id, toAll or toNone
	super    bool
	border   *autoBorder
	specific []specificCase
}

// GroundBrush paints ground with automatic borders.
type GroundBrush struct {
	id            int
	Name          string
	look          uint16
	items         chanceList
	zOrder        int
	randomize     bool
	soloOptional  bool
	optional      *autoBorder
	borders       []*borderBlock
	friends       []int
	hateFriends   bool
	hasOuter      bool
	hasInner      bool
	hasOuterZilch bool
	hasInnerZilch bool
}

func (g *GroundBrush) hasOuterBorder() bool      { return g.hasOuter || g.optional != nil }
func (g *GroundBrush) hasOuterZilchBorder() bool { return g.hasOuterZilch || g.optional != nil }

// friendOf reports whether other is a friend (or not an enemy).
func friendOf(friends []int, hate bool, other int) bool {
	found := slices.Contains(friends, other) || slices.Contains(friends, toAll)
	if hate {
		return !found
	}
	return found
}

func (g *GroundBrush) friendOf(o *GroundBrush) bool { return friendOf(g.friends, g.hateFriends, o.id) }

type wallNode struct {
	items chanceList
	doors []wallDoor
}

type wallDoor struct {
	id       uint16
	doorType int
	open     bool
	hate     bool
}

// WallBrush paints walls that connect to their neighbors.
type WallBrush struct {
	id          int
	Name        string
	look        uint16
	nodes       [wallAlignmentMax]wallNode
	redirect    *WallBrush
	friends     []int
	decoration  bool
	redirectsTo string
}

func (w *WallBrush) friendOf(o *WallBrush) bool { return friendOf(w.friends, false, o.id) }

// pick returns a random item of an alignment, following the redirect chain.
func (w *WallBrush) pick(align int, r *rand.Rand) uint16 {
	for b, n := w, 0; b != nil && n < 8; b, n = b.redirect, n+1 {
		if id := b.nodes[align].items.pick(r); id != 0 {
			return id
		}
	}
	return 0
}

func (w *WallBrush) hasWall(id uint16, align int) bool {
	for b, n := w, 0; b != nil && n < 8; b, n = b.redirect, n+1 {
		if b.nodes[align].items.has(id) {
			return true
		}
		for _, d := range b.nodes[align].doors {
			if d.id == id {
				return true
			}
		}
	}
	return false
}

// CarpetBrush paints carpets that join with their neighbors.
type CarpetBrush struct {
	id    int
	Name  string
	look  uint16
	parts [edgeCount]chanceList
}

// TableBrush paints tables that join with their neighbors.
type TableBrush struct {
	id    int
	Name  string
	look  uint16
	parts [tableCount]chanceList
}

type doodadSingle struct {
	chance int
	item   materials.ItemSpec
}

type doodadComposite struct {
	chance int
	tiles  []materials.CompositeTile
}

type doodadAlt struct {
	singles         []doodadSingle
	singleTotal     int
	composites      []doodadComposite
	compositeTotal  int
	ownedItems      map[uint16]bool
	hasComposite    bool
	hasSingleObject bool
}

// DoodadBrush places random decoration.
type DoodadBrush struct {
	id    int
	Name  string
	look  uint16
	opts  materials.DoodadOptions
	alts  []*doodadAlt
	owned map[uint16]bool
}

func (d *DoodadBrush) alt(variation int) *doodadAlt {
	if len(d.alts) == 0 {
		return nil
	}
	if variation < 0 {
		variation = -variation
	}
	return d.alts[variation%len(d.alts)]
}

func (d *DoodadBrush) owns(id uint16) bool { return d.owned[id] }

// itemInfo is what the brushes know about one item id.
type itemInfo struct {
	ground           *GroundBrush
	border           bool
	borderAlign      int
	borderGroup      uint16
	optionalBorder   bool
	groundEquivalent uint16
	wall             *WallBrush
	wallAlign        int
	door             bool
	doorType         int
	doorOpen         bool
	wallHate         bool
	carpet           *CarpetBrush
	carpetAlign      int
	table            *TableBrush
	tableAlign       int
	doodad           *DoodadBrush
}

// Brushes indexes materials for drawing.
type Brushes struct {
	items     *otitems.DB
	Materials *materials.Materials
	grounds   map[string]*GroundBrush
	walls     map[string]*WallBrush
	carpets   map[string]*CarpetBrush
	tables    map[string]*TableBrush
	doodads   map[string]*DoodadBrush
	info      map[uint16]*itemInfo
	nextID    int
	// Warnings collected while indexing.
	Warnings []string
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NewBrushes indexes materials. Unknown item ids are skipped with a warning.
func NewBrushes(items *otitems.DB, m *materials.Materials) *Brushes {
	if m == nil {
		m = materials.New()
	}
	b := &Brushes{
		items: items, Materials: m,
		grounds: map[string]*GroundBrush{}, walls: map[string]*WallBrush{}, carpets: map[string]*CarpetBrush{},
		tables: map[string]*TableBrush{}, doodads: map[string]*DoodadBrush{}, info: map[uint16]*itemInfo{},
	}
	items.MarkMeta(m.MetaItems)
	borders := map[uint32]*autoBorder{}
	for _, mb := range m.Borders {
		ab := b.autoBorder(mb)
		borders[mb.ID] = ab
		b.registerBorder(ab, mb.Optional)
	}
	// First pass: create brushes so rules can reference any brush by name.
	for _, mb := range m.Brushes {
		key := lower(mb.Name)
		b.nextID++
		switch mb.Type {
		case materials.TypeGround:
			b.grounds[key] = &GroundBrush{id: b.nextID, Name: mb.Name, look: mb.Look}
		case materials.TypeWall, materials.TypeDecoration:
			if len(mb.Walls) > 0 {
				b.walls[key] = &WallBrush{id: b.nextID, Name: mb.Name, look: mb.Look, decoration: mb.Type == materials.TypeDecoration}
			}
		case materials.TypeCarpet:
			b.carpets[key] = &CarpetBrush{id: b.nextID, Name: mb.Name, look: mb.Look}
		case materials.TypeTable:
			b.tables[key] = &TableBrush{id: b.nextID, Name: mb.Name, look: mb.Look}
		}
		if len(mb.Alternatives) > 0 {
			b.nextID++
			b.doodads[key] = &DoodadBrush{id: b.nextID, Name: mb.Name, look: mb.Look, owned: map[uint16]bool{}}
		}
	}
	idOf := func(name string) int {
		switch lower(name) {
		case "all":
			return toAll
		case "none", "":
			return toNone
		}
		k := lower(name)
		if g := b.grounds[k]; g != nil {
			return g.id
		}
		if w := b.walls[k]; w != nil {
			return w.id
		}
		return -2 // unknown: matches nothing
	}
	for _, mb := range m.Brushes {
		key := lower(mb.Name)
		if g := b.grounds[key]; g != nil {
			b.buildGround(g, mb, borders, idOf)
		}
		if w := b.walls[key]; w != nil {
			b.buildWall(w, mb, idOf)
		}
		if c := b.carpets[key]; c != nil {
			for align, cs := range mb.Parts {
				if i, ok := edgeIndex[align]; ok {
					c.parts[i] = newChanceList(cs)
					for _, x := range cs {
						in := b.infoFor(x.ID)
						in.carpet, in.carpetAlign = c, i
					}
				}
			}
			if c.look == 0 && len(c.parts[edgeCenter].ids) > 0 {
				c.look = c.parts[edgeCenter].ids[0]
			}
		}
		if t := b.tables[key]; t != nil {
			for align, cs := range mb.Parts {
				i := slices.Index(materials.TableAlignments, align)
				if i < 0 {
					continue
				}
				t.parts[i] = newChanceList(cs)
				for _, x := range cs {
					in := b.infoFor(x.ID)
					in.table, in.tableAlign = t, i
				}
			}
		}
		if d := b.doodads[key]; d != nil {
			b.buildDoodad(d, mb)
		}
	}
	for _, w := range b.walls {
		if w.redirectsTo != "" {
			w.redirect = b.walls[lower(w.redirectsTo)]
		}
	}
	return b
}

func (b *Brushes) infoFor(id uint16) *itemInfo {
	in := b.info[id]
	if in == nil {
		in = &itemInfo{}
		b.info[id] = in
	}
	return in
}

func (b *Brushes) autoBorder(mb *materials.Border) *autoBorder {
	ab := &autoBorder{id: mb.ID, group: mb.Group, groundEquivalent: mb.GroundEquivalent}
	for name, id := range mb.Edges {
		if i, ok := edgeIndex[name]; ok && i < edgeCenter {
			ab.tiles[i] = id
		}
	}
	return ab
}

func (b *Brushes) registerBorder(ab *autoBorder, optional bool) {
	for edge, id := range ab.tiles {
		if id == 0 {
			continue
		}
		in := b.infoFor(id)
		in.border = true
		if in.borderAlign == edgeNone {
			in.borderAlign = edge
		}
		if ab.group != 0 && in.borderGroup == 0 {
			in.borderGroup = ab.group
		}
		if optional {
			in.optionalBorder = true
		}
		if ab.groundEquivalent != 0 {
			in.groundEquivalent = ab.groundEquivalent
		}
	}
}

func (b *Brushes) buildGround(g *GroundBrush, mb *materials.Brush, borders map[uint32]*autoBorder, idOf func(string) int) {
	g.items = newChanceList(mb.Items)
	g.zOrder = mb.ZOrder
	g.randomize = !mb.NoRandomize && g.items.total > 0
	g.soloOptional = mb.SoloOptional
	for _, x := range mb.Items {
		b.infoFor(x.ID).ground = g
	}
	if g.look == 0 && len(g.items.ids) > 0 {
		g.look = g.items.ids[0]
	}
	resolve := func(r materials.BorderRule) *autoBorder {
		if r.Inline != nil {
			ab := b.autoBorder(r.Inline)
			b.registerBorder(ab, false)
			if ab.groundEquivalent != 0 {
				b.infoFor(ab.groundEquivalent).ground = g
			}
			return ab
		}
		if r.Border == 0 {
			return nil
		}
		ab := borders[r.Border]
		if ab == nil {
			b.Warnings = append(b.Warnings, "brush "+g.Name+": unknown border")
		}
		return ab
	}
	if mb.Optional != nil {
		g.optional = resolve(*mb.Optional)
		if g.optional != nil {
			for _, id := range g.optional.tiles {
				if id != 0 {
					b.infoFor(id).optionalBorder = true
				}
			}
		}
	}
	for _, r := range mb.Borders {
		bb := &borderBlock{outer: r.Align != "inner", super: r.Super, border: resolve(r)}
		bb.to = idOf(r.To)
		if r.To == "" {
			bb.to = toAll
		}
		for _, sc := range r.Specific {
			bb.specific = append(bb.specific, b.specificCase(sc, borders))
		}
		if bb.outer {
			if bb.to == toNone {
				g.hasOuterZilch = true
			} else {
				g.hasOuter = true
			}
		} else {
			if bb.to == toNone {
				g.hasInnerZilch = true
			} else {
				g.hasInner = true
			}
		}
		g.borders = append(g.borders, bb)
	}
	for _, f := range mb.Friends {
		if id := idOf(f); id != toNone {
			g.friends = append(g.friends, id)
		}
	}
	g.hateFriends = mb.HateFriends
}

func (b *Brushes) specificCase(sc materials.SpecificCase, borders map[uint32]*autoBorder) specificCase {
	var out specificCase
	edgeItem := func(border uint32, edge string) uint16 {
		ab := borders[border]
		if ab == nil {
			return 0
		}
		return ab.tiles[edgeIndex[edge]]
	}
	for _, c := range sc.Conditions {
		switch c.Kind {
		case "border":
			out.match = append(out.match, edgeItem(c.Border, c.Edge))
		case "group":
			out.matchGroup = c.Group
			out.groupAlign = edgeIndex[c.Edge]
			out.match = append(out.match, c.Group)
		case "item":
			out.match = append(out.match, c.Item)
		}
	}
	for _, a := range sc.Actions {
		switch a.Kind {
		case "replaceBorder":
			out.replace, out.with = edgeItem(a.Border, a.Edge), a.With
		case "replaceItem":
			out.replace, out.with = a.Item, a.With
		case "deleteBorders":
			out.deleteAll = true
		}
		if out.with != 0 {
			b.infoFor(out.with).border = true
		}
	}
	return out
}

func (b *Brushes) buildWall(w *WallBrush, mb *materials.Brush, idOf func(string) int) {
	for align, part := range mb.Walls {
		i := slices.Index(materials.WallAlignments, align)
		if i < 0 {
			continue
		}
		w.nodes[i].items = newChanceList(part.Items)
		for _, x := range part.Items {
			in := b.infoFor(x.ID)
			in.wall, in.wallAlign = w, i
		}
		for _, d := range part.Doors {
			dt := slices.Index(materials.DoorTypes, d.Type) + 1
			w.nodes[i].doors = append(w.nodes[i].doors, wallDoor{id: d.ID, doorType: dt, open: d.Open, hate: d.Hate})
			in := b.infoFor(d.ID)
			in.wall, in.wallAlign, in.door, in.doorOpen, in.wallHate = w, i, true, d.Open, d.Hate
			if in.doorType == doorUndefined {
				in.doorType = dt
			}
		}
	}
	for _, f := range mb.Friends {
		if id := idOf(f); id > 0 {
			w.friends = append(w.friends, id)
		}
	}
	w.redirectsTo = mb.Redirect
	if w.look == 0 {
		for _, a := range []int{wallHorizontal, wallVertical, wallPole} {
			if len(w.nodes[a].items.ids) > 0 {
				w.look = w.nodes[a].items.ids[0]
				break
			}
		}
	}
}

func (b *Brushes) buildDoodad(d *DoodadBrush, mb *materials.Brush) {
	if mb.Doodad != nil {
		d.opts = *mb.Doodad
	}
	for _, a := range mb.Alternatives {
		alt := &doodadAlt{ownedItems: map[uint16]bool{}}
		for _, s := range a.Singles {
			alt.singles = append(alt.singles, doodadSingle{chance: s.Chance, item: s.Item})
			alt.singleTotal += max(s.Chance, 0)
			alt.ownedItems[s.Item.ID] = true
		}
		for _, c := range a.Composites {
			alt.compositeTotal += max(c.Chance, 0)
			alt.composites = append(alt.composites, doodadComposite{chance: alt.compositeTotal, tiles: c.Tiles})
			for _, t := range c.Tiles {
				for _, it := range t.Items {
					alt.ownedItems[it.ID] = true
				}
			}
		}
		alt.hasComposite = len(alt.composites) > 0
		alt.hasSingleObject = len(alt.singles) > 0
		for id := range alt.ownedItems {
			d.owned[id] = true
			b.infoFor(id).doodad = d
		}
		d.alts = append(d.alts, alt)
	}
	if d.look == 0 && len(d.alts) > 0 {
		if len(d.alts[0].singles) > 0 {
			d.look = d.alts[0].singles[0].item.ID
		} else if len(d.alts[0].composites) > 0 && len(d.alts[0].composites[0].tiles) > 0 {
			d.look = d.alts[0].composites[0].tiles[0].Items[0].ID
		}
	}
}

// Ground returns a ground brush by name.
func (b *Brushes) Ground(name string) *GroundBrush { return b.grounds[lower(name)] }

// Wall returns a wall brush by name.
func (b *Brushes) Wall(name string) *WallBrush { return b.walls[lower(name)] }

// Carpet returns a carpet brush by name.
func (b *Brushes) Carpet(name string) *CarpetBrush { return b.carpets[lower(name)] }

// Table returns a table brush by name.
func (b *Brushes) Table(name string) *TableBrush { return b.tables[lower(name)] }

// Doodad returns a doodad brush by name.
func (b *Brushes) Doodad(name string) *DoodadBrush { return b.doodads[lower(name)] }

// GroundOf returns the ground brush of an item id.
func (b *Brushes) GroundOf(id uint16) *GroundBrush {
	if in := b.info[id]; in != nil {
		return in.ground
	}
	return nil
}

// BrushOf returns the name and kind of the brush that owns an item, for
// "pick brush" (empty when none).
func (b *Brushes) BrushOf(id uint16) (name, kind string) {
	in := b.info[id]
	switch {
	case in == nil:
		return "", ""
	case in.ground != nil:
		return in.ground.Name, materials.TypeGround
	case in.door && in.wall != nil:
		return in.wall.Name, materials.TypeWall
	case in.wall != nil:
		if in.wall.decoration {
			return in.wall.Name, materials.TypeDecoration
		}
		return in.wall.Name, materials.TypeWall
	case in.carpet != nil:
		return in.carpet.Name, materials.TypeCarpet
	case in.table != nil:
		return in.table.Name, materials.TypeTable
	case in.doodad != nil:
		return in.doodad.Name, materials.TypeDoodad
	}
	return "", ""
}

// Look returns the palette item of a brush.
func (b *Brushes) Look(name string) uint16 {
	k := lower(name)
	switch {
	case b.grounds[k] != nil:
		return b.grounds[k].look
	case b.walls[k] != nil:
		return b.walls[k].look
	case b.carpets[k] != nil:
		return b.carpets[k].look
	case b.tables[k] != nil:
		return b.tables[k].look
	case b.doodads[k] != nil:
		return b.doodads[k].look
	}
	return 0
}

func (b *Brushes) isBorder(id uint16) bool {
	if in := b.info[id]; in != nil && in.border {
		return true
	}
	t := b.items.Get(id)
	return t != nil && t.TopOrder == 1
}

func (b *Brushes) wallOf(id uint16) (*WallBrush, *itemInfo) {
	in := b.info[id]
	if in == nil || in.wall == nil {
		return nil, nil
	}
	return in.wall, in
}
