// Package otmap is the Open Tibia map model (OTBM) with its auxiliary files:
// houses, spawns (one file with monsters and NPCs, or separate
// monster and NPC files) and zones.
//
// Tiles live in 32x32 chunks per floor so drawing and saving touch only what
// exists, and items are small values: the rarely used attributes live in a
// separate struct, so a large map stays compact in memory.
package otmap

import (
	"cmp"
	"slices"
)

// Map limits.
const (
	MaxFloor    = 15
	GroundFloor = 7
	ChunkBits   = 5
	ChunkSize   = 1 << ChunkBits
)

// Position is a map position.
type Position struct {
	X uint16 `json:"x"`
	Y uint16 `json:"y"`
	Z uint8  `json:"z"`
}

// Add returns p moved by dx, dy, dz; ok is false when the result leaves the
// valid range.
func (p Position) Add(dx, dy, dz int) (Position, bool) {
	x, y, z := int(p.X)+dx, int(p.Y)+dy, int(p.Z)+dz
	if x < 0 || y < 0 || z < 0 || x > 0xFFFF || y > 0xFFFF || z > MaxFloor {
		return Position{}, false
	}
	return Position{uint16(x), uint16(y), uint8(z)}, true
}

// Tile map flags (OTBM tile flags, TFS layout).
const (
	FlagPZ       uint32 = 1 << 0
	FlagNoPVP    uint32 = 1 << 2
	FlagNoLogout uint32 = 1 << 3
	FlagPVPZone  uint32 = 1 << 4
	FlagRefresh  uint32 = 1 << 5
)

// Item is one item on the map. Subtype is the count, charges or fluid type.
type Item struct {
	ID       uint16   `json:"id"`
	Subtype  uint16   `json:"subtype"`
	Selected bool     `json:"selected"`
	Ext      *ItemExt `json:"ext,omitempty"`
}

// ItemExt holds attributes few items have.
type ItemExt struct {
	ActionID    uint16    `json:"actionId,omitempty"`
	UniqueID    uint16    `json:"uniqueId,omitempty"`
	Text        string    `json:"text,omitempty"`
	Description string    `json:"description,omitempty"`
	Dest        *Position `json:"dest,omitempty"`
	DepotID     uint16    `json:"depotId,omitempty"`
	DoorID      uint8     `json:"doorId,omitempty"`
	// Items are the contents of a container.
	Items []Item `json:"items,omitempty"`
	// Attrs is the OTBM attribute map (custom attributes).
	Attrs []Attr `json:"attrs,omitempty"`
	// Raw keeps attributes the editor does not edit (written date, duration,
	// decay state...) to write them back unchanged.
	Raw []RawAttr `json:"raw,omitempty"`
}

// Attribute value types of the OTBM attribute map.
const (
	AttrString  uint8 = 1
	AttrInteger uint8 = 2
	AttrFloat   uint8 = 3
	AttrBoolean uint8 = 4
	AttrDouble  uint8 = 5
)

// Attr is one custom attribute.
type Attr struct {
	Key   string  `json:"key"`
	Type  uint8   `json:"type"`
	Str   string  `json:"str,omitempty"`
	Num   float64 `json:"num,omitempty"`
	Int   int32   `json:"int,omitempty"`
	Value bool    `json:"value,omitempty"`
}

// RawAttr is an OTBM item attribute kept as bytes.
type RawAttr struct {
	Code byte   `json:"code"`
	Data []byte `json:"data"`
}

func (e *ItemExt) empty() bool {
	return e == nil || (e.ActionID == 0 && e.UniqueID == 0 && e.Text == "" && e.Description == "" && e.Dest == nil &&
		e.DepotID == 0 && e.DoorID == 0 && len(e.Items) == 0 && len(e.Attrs) == 0 && len(e.Raw) == 0)
}

// Ensure returns the item's ext, creating it.
func (it *Item) Ensure() *ItemExt {
	if it.Ext == nil {
		it.Ext = &ItemExt{}
	}
	return it.Ext
}

// Compact drops an empty ext.
func (it *Item) Compact() {
	if it.Ext.empty() {
		it.Ext = nil
	}
}

// ActionID returns the action id.
func (it *Item) ActionID() uint16 {
	if it.Ext == nil {
		return 0
	}
	return it.Ext.ActionID
}

// UniqueID returns the unique id.
func (it *Item) UniqueID() uint16 {
	if it.Ext == nil {
		return 0
	}
	return it.Ext.UniqueID
}

// Complex reports whether the item has attributes besides its id and
// subtype (the eraser can keep those).
func (it *Item) Complex() bool { return !it.Ext.empty() }

// Clone returns a deep copy.
func (it Item) Clone() Item {
	if it.Ext == nil {
		return it
	}
	e := *it.Ext
	if e.Dest != nil {
		d := *e.Dest
		e.Dest = &d
	}
	if e.Items != nil {
		e.Items = cloneItems(e.Items)
	}
	e.Attrs = slices.Clone(e.Attrs)
	if e.Raw != nil {
		raw := make([]RawAttr, len(e.Raw))
		for i, r := range e.Raw {
			raw[i] = RawAttr{Code: r.Code, Data: slices.Clone(r.Data)}
		}
		e.Raw = raw
	}
	it.Ext = &e
	return it
}

func cloneItems(items []Item) []Item {
	if items == nil {
		return nil
	}
	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = it.Clone()
	}
	return out
}

// Creature is a monster or NPC placed by a spawn.
type Creature struct {
	Name      string `json:"name"`
	NPC       bool   `json:"npc"`
	SpawnTime int32  `json:"spawnTime"`
	Direction uint8  `json:"direction"`
	// Weight is the spawn weight of separate monster files (0 = default).
	Weight   uint8 `json:"weight,omitempty"`
	Selected bool  `json:"selected"`
}

// Spawn is a spawn area centered on its tile.
type Spawn struct {
	Radius   int32 `json:"radius"`
	Selected bool  `json:"selected"`
}

// Tile state bits (editor only, never saved).
const (
	StateModified uint8 = 1 << 0
	// StateOptionalBorder asks the ground brush to add the optional border
	// (mountain) of a neighbor.
	StateOptionalBorder uint8 = 1 << 1
)

// Tile is one map tile.
type Tile struct {
	Ground  Item     `json:"ground"`
	Items   []Item   `json:"items"`
	Flags   uint32   `json:"flags"`
	HouseID uint32   `json:"houseId"`
	State   uint8    `json:"state"`
	Ext     *TileExt `json:"ext,omitempty"`
}

// TileExt holds what few tiles have.
type TileExt struct {
	Creatures []Creature `json:"creatures,omitempty"`
	Spawn     *Spawn     `json:"spawn,omitempty"`
	// SpawnNPC is a separate NPC spawn area.
	SpawnNPC *Spawn   `json:"spawnNpc,omitempty"`
	Zones    []uint16 `json:"zones,omitempty"`
}

func (e *TileExt) empty() bool {
	return e == nil || (len(e.Creatures) == 0 && e.Spawn == nil && e.SpawnNPC == nil && len(e.Zones) == 0)
}

// EnsureExt returns the tile's ext, creating it.
func (t *Tile) EnsureExt() *TileExt {
	if t.Ext == nil {
		t.Ext = &TileExt{}
	}
	return t.Ext
}

// Compact drops an empty ext.
func (t *Tile) Compact() {
	if t.Ext.empty() {
		t.Ext = nil
	}
}

// Empty reports whether the tile holds nothing worth saving.
func (t *Tile) Empty() bool {
	return t == nil || (t.Ground.ID == 0 && len(t.Items) == 0 && t.Flags == 0 && t.HouseID == 0 && t.Ext.empty())
}

// Creatures returns the creatures on the tile.
func (t *Tile) Creatures() []Creature {
	if t.Ext == nil {
		return nil
	}
	return t.Ext.Creatures
}

// Spawn returns the monster spawn on the tile.
func (t *Tile) Spawn() *Spawn {
	if t.Ext == nil {
		return nil
	}
	return t.Ext.Spawn
}

// SpawnNPC returns the NPC spawn on the tile.
func (t *Tile) SpawnNPC() *Spawn {
	if t.Ext == nil {
		return nil
	}
	return t.Ext.SpawnNPC
}

// Zones returns the zone ids of the tile.
func (t *Tile) Zones() []uint16 {
	if t.Ext == nil {
		return nil
	}
	return t.Ext.Zones
}

// Clone returns a deep copy.
func (t *Tile) Clone() *Tile {
	if t == nil {
		return nil
	}
	c := *t
	c.Ground = t.Ground.Clone()
	c.Items = cloneItems(t.Items)
	if t.Ext != nil {
		e := *t.Ext
		e.Creatures = slices.Clone(e.Creatures)
		if e.Spawn != nil {
			s := *e.Spawn
			e.Spawn = &s
		}
		if e.SpawnNPC != nil {
			s := *e.SpawnNPC
			e.SpawnNPC = &s
		}
		e.Zones = slices.Clone(e.Zones)
		c.Ext = &e
	}
	return &c
}

// Selected reports whether anything on the tile is selected.
func (t *Tile) Selected() bool {
	if t == nil {
		return false
	}
	if t.Ground.Selected {
		return true
	}
	for i := range t.Items {
		if t.Items[i].Selected {
			return true
		}
	}
	if t.Ext != nil {
		for _, c := range t.Ext.Creatures {
			if c.Selected {
				return true
			}
		}
		if t.Ext.Spawn != nil && t.Ext.Spawn.Selected {
			return true
		}
		if t.Ext.SpawnNPC != nil && t.Ext.SpawnNPC.Selected {
			return true
		}
	}
	return false
}

// SetSelected selects or deselects everything on the tile.
func (t *Tile) SetSelected(on bool) {
	if t.Ground.ID != 0 {
		t.Ground.Selected = on
	}
	for i := range t.Items {
		t.Items[i].Selected = on
	}
	if t.Ext != nil {
		for i := range t.Ext.Creatures {
			t.Ext.Creatures[i].Selected = on
		}
		if t.Ext.Spawn != nil {
			t.Ext.Spawn.Selected = on
		}
		if t.Ext.SpawnNPC != nil {
			t.Ext.SpawnNPC.Selected = on
		}
	}
}

// ItemCount returns the number of items including the ground.
func (t *Tile) ItemCount() int {
	n := len(t.Items)
	if t.Ground.ID != 0 {
		n++
	}
	return n
}

// House is a house with its metadata from houses.xml.
type House struct {
	ID        uint32   `json:"id"`
	Name      string   `json:"name"`
	TownID    uint32   `json:"townId"`
	Rent      int32    `json:"rent"`
	Guildhall bool     `json:"guildhall"`
	Exit      Position `json:"exit"`
	// ClientID and Beds are attributes of newer servers.
	ClientID uint32 `json:"clientId,omitempty"`
	Beds     int32  `json:"beds,omitempty"`
}

// Clone returns a copy.
func (h *House) Clone() *House {
	c := *h
	return &c
}

// Town is a town with its temple position.
type Town struct {
	ID     uint32   `json:"id"`
	Name   string   `json:"name"`
	Temple Position `json:"temple"`
}

// Waypoint is a named position.
type Waypoint struct {
	Name string   `json:"name"`
	Pos  Position `json:"pos"`
}

// Zone is a named zone.
type Zone struct {
	ID   uint16 `json:"id"`
	Name string `json:"name"`
}

// SpawnFormat tells how spawns are stored.
type SpawnFormat string

const (
	// SpawnsSingle is one spawn file with monsters and NPCs.
	SpawnsSingle SpawnFormat = "single"
	// SpawnsSeparate are separate monster and NPC spawn files.
	SpawnsSeparate SpawnFormat = "separate"
)

// ChunkKey identifies a chunk.
type ChunkKey uint32

// KeyOf returns the key of the chunk holding a position.
func KeyOf(p Position) ChunkKey {
	return MakeKey(p.X>>ChunkBits, p.Y>>ChunkBits, p.Z)
}

// MakeKey builds a key from chunk coordinates.
func MakeKey(cx, cy uint16, z uint8) ChunkKey {
	return ChunkKey(uint32(cx)<<15 | uint32(cy)<<4 | uint32(z&15))
}

// Split returns the chunk coordinates of a key.
func (k ChunkKey) Split() (cx, cy uint16, z uint8) {
	return uint16(k >> 15), uint16(k>>4) & 0x7FF, uint8(k & 15)
}

// OTBMVersion is the only OTBM version the editor writes: version 2, which
// servers load (they reject newer ones). Files of versions 1 to 4 are
// read and saved as version 2. Waypoints are written too: servers read them
// in any version.
const OTBMVersion uint32 = 2

// otbmNewest is the newest OTBM version the reader knows.
const otbmNewest uint32 = 4

// Map is an OTBM map. It is not safe for concurrent use.
type Map struct {
	Width       uint16      `json:"width"`
	Height      uint16      `json:"height"`
	ItemsMajor  uint32      `json:"itemsMajor"`
	ItemsMinor  uint32      `json:"itemsMinor"`
	Description string      `json:"description"`
	SpawnFile   string      `json:"spawnFile"`
	NpcFile     string      `json:"npcFile"`
	HouseFile   string      `json:"houseFile"`
	ZoneFile    string      `json:"zoneFile"`
	SpawnFormat SpawnFormat `json:"spawnFormat"`

	Towns     map[uint32]*Town  `json:"-"`
	Houses    map[uint32]*House `json:"-"`
	Waypoints []*Waypoint       `json:"-"`
	Zones     []Zone            `json:"-"`

	chunks map[ChunkKey]*Chunk
	tiles  int
	floors [MaxFloor + 1]int
	// Rev increases on every tile change (see Touch).
	Rev uint64 `json:"-"`
}

// New returns an empty map.
func New(width, height uint16) *Map {
	return &Map{
		Width: width, Height: height, SpawnFormat: SpawnsSingle,
		Towns: map[uint32]*Town{}, Houses: map[uint32]*House{},
		chunks: map[ChunkKey]*Chunk{},
	}
}

// TileCount returns the number of tiles.
func (m *Map) TileCount() int { return m.tiles }

// FloorTiles returns the number of tiles on each floor.
func (m *Map) FloorTiles() [MaxFloor + 1]int { return m.floors }

// Tile returns the tile at p or nil. Treat it as read-only: a packed tile
// (see chunk.go) comes back as a new copy.
func (m *Map) Tile(p Position) *Tile {
	c := m.chunks[KeyOf(p)]
	if c == nil {
		return nil
	}
	return c.get(localIndex(p))
}

// Has reports whether there is a tile at p.
func (m *Map) Has(p Position) bool {
	c := m.chunks[KeyOf(p)]
	return c != nil && c.cells[localIndex(p)] != 0
}

// At returns the tile at x, y, z or nil; out of range coordinates are nil.
func (m *Map) At(x, y, z int) *Tile {
	if x < 0 || y < 0 || z < 0 || x > 0xFFFF || y > 0xFFFF || z > MaxFloor {
		return nil
	}
	return m.Tile(Position{uint16(x), uint16(y), uint8(z)})
}

func localIndex(p Position) int {
	return int(p.Y&(ChunkSize-1))<<ChunkBits | int(p.X&(ChunkSize-1))
}

// SetTile stores a tile (nil or empty removes it) and marks the chunk
// changed. The map may pack the tile: keep no pointer to it.
func (m *Map) SetTile(p Position, t *Tile) {
	if t != nil && t.Empty() {
		t = nil
	}
	m.put(p, t, true)
}

// Put stores a tile even when it is empty (an editor working copy); nil
// removes the tile. Prune drops it again when it stays empty. The map keeps
// t itself, so changes to it show on the map.
func (m *Map) Put(p Position, t *Tile) { m.put(p, t, false) }

func (m *Map) put(p Position, t *Tile, pack bool) {
	k := KeyOf(p)
	c := m.chunks[k]
	if c == nil {
		if t == nil {
			return
		}
		cx, cy, z := k.Split()
		c = &Chunk{X: cx, Y: cy, Z: z}
		m.chunks[k] = c
	}
	had := c.set(localIndex(p), t, pack)
	switch {
	case !had && t != nil:
		c.Count++
		m.tiles++
		m.floors[p.Z]++
	case had && t == nil:
		c.Count--
		m.tiles--
		m.floors[p.Z]--
	}
	m.Rev++
	c.Rev = m.Rev
	if c.Count == 0 {
		delete(m.chunks, k)
	}
}

// Prune removes the tile at p when it is empty.
func (m *Map) Prune(p Position) {
	if t := m.Tile(p); t != nil && t.Empty() {
		m.Put(p, nil)
	}
}

// Touch marks the chunk of p changed (after editing a tile in place).
func (m *Map) Touch(p Position) {
	if c := m.chunks[KeyOf(p)]; c != nil {
		m.Rev++
		c.Rev = m.Rev
	}
}

// Chunk returns a chunk or nil.
func (m *Map) Chunk(k ChunkKey) *Chunk { return m.chunks[k] }

// ChunkRev returns the revision of a chunk (0 when it does not exist).
func (m *Map) ChunkRev(k ChunkKey) uint64 {
	if c := m.chunks[k]; c != nil {
		return c.Rev
	}
	return 0
}

// Chunks returns all chunks sorted by floor, then by 256x256 area, then by
// position: the order OTBM tile areas are written in.
func (m *Map) Chunks() []*Chunk {
	out := make([]*Chunk, 0, len(m.chunks))
	for _, c := range m.chunks {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *Chunk) int {
		return cmp.Or(
			cmp.Compare(a.Z, b.Z),
			cmp.Compare(a.Y>>3, b.Y>>3),
			cmp.Compare(a.X>>3, b.X>>3),
			cmp.Compare(a.Y, b.Y),
			cmp.Compare(a.X, b.X),
		)
	})
	return out
}

// Each calls fn for every tile in save order. fn must not add or remove
// tiles; t is only valid during the call (see Chunk.Each).
func (m *Map) Each(fn func(p Position, t *Tile)) {
	for _, c := range m.Chunks() {
		c.Each(fn)
	}
}

// EachIn calls fn for every tile inside the rectangle on floors z0..z1.
func (m *Map) EachIn(x0, y0, x1, y1 int, z0, z1 int, fn func(p Position, t *Tile)) {
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, 0xFFFF), min(y1, 0xFFFF)
	for z := max(z0, 0); z <= min(z1, MaxFloor); z++ {
		for cy := y0 >> ChunkBits; cy <= y1>>ChunkBits; cy++ {
			for cx := x0 >> ChunkBits; cx <= x1>>ChunkBits; cx++ {
				c := m.chunks[MakeKey(uint16(cx), uint16(cy), uint8(z))]
				if c == nil {
					continue
				}
				c.Each(func(p Position, t *Tile) {
					if int(p.X) >= x0 && int(p.X) <= x1 && int(p.Y) >= y0 && int(p.Y) <= y1 {
						fn(p, t)
					}
				})
			}
		}
	}
}

// Bounds returns the area that holds tiles (ok is false for an empty map).
func (m *Map) Bounds() (minX, minY, maxX, maxY int, ok bool) {
	first := true
	for _, c := range m.chunks {
		c.Each(func(p Position, _ *Tile) {
			x, y := int(p.X), int(p.Y)
			if first {
				minX, minY, maxX, maxY, first = x, y, x, y, false
				return
			}
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		})
	}
	return minX, minY, maxX, maxY, !first
}

// SortedTowns returns the towns by id.
func (m *Map) SortedTowns() []*Town {
	out := make([]*Town, 0, len(m.Towns))
	for _, t := range m.Towns {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b *Town) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// SortedHouses returns the houses by id.
func (m *Map) SortedHouses() []*House {
	out := make([]*House, 0, len(m.Houses))
	for _, h := range m.Houses {
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b *House) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Waypoint returns the waypoint with a name or nil.
func (m *Map) Waypoint(name string) *Waypoint {
	for _, w := range m.Waypoints {
		if w.Name == name {
			return w
		}
	}
	return nil
}
