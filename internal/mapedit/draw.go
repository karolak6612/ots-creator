package mapedit

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/nekiro/ots-creator/internal/materials"
	"github.com/nekiro/ots-creator/internal/otitems"
	"github.com/nekiro/ots-creator/internal/otmap"
)

// Brush kinds of a draw request.
const (
	KindRaw            = "raw"
	KindGround         = "ground"
	KindWall           = "wall"
	KindDoor           = "door"
	KindDoodad         = "doodad"
	KindCarpet         = "carpet"
	KindTable          = "table"
	KindEraser         = "eraser"
	KindHouse          = "house"
	KindHouseExit      = "houseExit"
	KindWaypoint       = "waypoint"
	KindSpawn          = "spawn"
	KindSpawnNPC       = "spawnNpc"
	KindCreature       = "creature"
	KindFlag           = "flag"
	KindZone           = "zone"
	KindOptionalBorder = "optionalBorder"
)

// Rect is a rectangle of tiles on one floor (corners in any order).
type Rect struct {
	From otmap.Position `json:"from"`
	To   otmap.Position `json:"to"`
}

func (r Rect) bounds() (x0, y0, x1, y1 int) {
	x0, x1 = int(min(r.From.X, r.To.X)), int(max(r.From.X, r.To.X))
	y0, y1 = int(min(r.From.Y, r.To.Y)), int(max(r.From.Y, r.To.Y))
	return
}

// DrawRequest applies a brush.
type DrawRequest struct {
	Kind string `json:"kind"`
	// Name is the brush name (ground, wall, doodad, carpet, table).
	Name string `json:"name"`
	// Item is the item of a raw brush.
	Item uint16 `json:"item"`
	// House is the house of house and house exit brushes.
	House uint32 `json:"house"`
	// Waypoint is the waypoint name.
	Waypoint string `json:"waypoint"`
	// Creature and NPC select the creature type.
	Creature  string `json:"creature"`
	NPC       bool   `json:"npc"`
	SpawnTime int32  `json:"spawnTime"`
	Direction uint8  `json:"direction"`
	// Flag is the tile flag of flag brushes, Zone the zone id.
	Flag uint32 `json:"flag"`
	Zone uint16 `json:"zone"`
	// DoorType is the door type of door brushes.
	DoorType string `json:"doorType"`

	// Points are the brush centers (a stroke may send several).
	Points []otmap.Position `json:"points"`
	Size   int              `json:"size"`
	// Shape is "square" or "circle".
	Shape string `json:"shape"`
	// Erase removes what the brush places (Ctrl).
	Erase bool `json:"erase"`
	// Alt: ground brushes only replace the ground they started on, walls
	// change shape on click, doors are placed open, doodads ignore
	// blocking tiles and duplicates.
	Alt bool `json:"alt"`
	// Fill flood fills the ground area under Points[0].
	Fill bool `json:"fill"`
	// Rect paints a rectangle (Shift+drag).
	Rect      *Rect   `json:"rect"`
	Variation int     `json:"variation"`
	Thickness float64 `json:"thickness"`
	// Stroke merges requests of one mouse stroke into one undo step.
	Stroke int64 `json:"stroke"`
}

// ErrNoBrush is returned for an unknown brush.
var ErrNoBrush = errors.New("unknown brush")

// maxDrawTiles limits one request.
const maxDrawTiles = 250000

type posSet struct {
	list []otmap.Position
	seen map[otmap.Position]bool
}

func newPosSet() *posSet { return &posSet{seen: map[otmap.Position]bool{}} }

func (s *posSet) add(p otmap.Position) {
	if !s.seen[p] {
		s.seen[p] = true
		s.list = append(s.list, p)
	}
}

func (s *posSet) addXY(x, y, z int) {
	if inRange(x, y) {
		s.add(pos(x, y, z))
	}
}

// ring returns the tiles around a set (including the set).
func (s *posSet) ring() *posSet {
	r := newPosSet()
	for _, p := range s.list {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				r.addXY(int(p.X)+dx, int(p.Y)+dy, int(p.Z))
			}
		}
	}
	return r
}

// BrushArea returns the tiles a brush of a size and shape covers around a
// center.
func BrushArea(center otmap.Position, size int, shape string) []otmap.Position {
	s := newPosSet()
	addArea(s, center, size, shape)
	return s.list
}

func addArea(s *posSet, c otmap.Position, size int, shape string) {
	size = max(size, 0)
	for dy := -size; dy <= size; dy++ {
		for dx := -size; dx <= size; dx++ {
			if shape == "circle" && math.Sqrt(float64(dx*dx+dy*dy)) >= float64(size)+0.005 {
				continue
			}
			s.addXY(int(c.X)+dx, int(c.Y)+dy, int(c.Z))
		}
	}
}

func addOutline(s *posSet, x0, y0, x1, y1, z int) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if x == x0 || x == x1 || y == y0 || y == y1 {
				s.addXY(x, y, z)
			}
		}
	}
}

// Draw applies a brush and returns the number of changed tiles.
func (e *Editor) Draw(r DrawRequest) (int, error) {
	label := strings.ToUpper(r.Kind[:1]) + r.Kind[1:]
	if r.Erase {
		label = "Erase " + r.Kind
	}
	if len(r.Points) == 0 && r.Rect == nil {
		return 0, nil
	}
	tx := e.Begin(label, r.Stroke)
	var err error
	switch r.Kind {
	case KindHouseExit, KindWaypoint:
		err = e.drawMarker(tx, r)
	case KindSpawn, KindSpawnNPC:
		e.drawSpawn(tx, r)
	case KindCreature:
		err = e.drawCreature(tx, r)
	case KindDoor:
		e.drawDoorRequest(tx, r)
	case KindDoodad:
		err = e.drawDoodadRequest(tx, r)
	default:
		err = e.drawArea(tx, r)
	}
	n := tx.Commit()
	return n, err
}

// area returns the tiles a request paints.
func (e *Editor) area(r DrawRequest) *posSet {
	s := newPosSet()
	if r.Rect != nil {
		x0, y0, x1, y1 := r.Rect.bounds()
		if (x1-x0+1)*(y1-y0+1) > maxDrawTiles {
			return s
		}
		z := int(r.Rect.From.Z)
		if r.Kind == KindWall {
			addOutline(s, x0, y0, x1, y1, z)
			return s
		}
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				s.addXY(x, y, z)
			}
		}
		return s
	}
	if r.Fill && r.Kind == KindGround && len(r.Points) > 0 {
		return e.floodFill(r.Points[0], r.Name)
	}
	for _, p := range r.Points {
		if r.Kind == KindWall && r.Size > 0 {
			addOutline(s, int(p.X)-r.Size, int(p.Y)-r.Size, int(p.X)+r.Size, int(p.Y)+r.Size, int(p.Z))
			continue
		}
		addArea(s, p, r.Size, r.Shape)
		if len(s.list) > maxDrawTiles {
			break
		}
	}
	return s
}

// floodFill returns the connected area of the ground brush under start
// (or of empty tiles), limited to a window around it.
func (e *Editor) floodFill(start otmap.Position, brush string) *posSet {
	s := newPosSet()
	const radius = 128
	startTile := e.Map.Tile(start)
	old := e.groundBrushOf(startTile)
	target := e.Brushes.Ground(brush)
	if target == nil || (old != nil && old.id == target.id) {
		return s
	}
	if startTile != nil && startTile.Ground.ID != 0 && old == nil {
		return s // a ground without brush: nothing to match
	}
	same := func(t *otmap.Tile) bool {
		if old == nil {
			return t == nil || t.Ground.ID == 0
		}
		g := e.groundBrushOf(t)
		return g != nil && g.id == old.id
	}
	queue := []otmap.Position{start}
	s.add(start)
	for len(queue) > 0 && len(s.list) < maxDrawTiles {
		p := queue[0]
		queue = queue[1:]
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := int(p.X)+d[0], int(p.Y)+d[1]
			if !inRange(nx, ny) || abs(nx-int(start.X)) > radius || abs(ny-int(start.Y)) > radius {
				continue
			}
			q := pos(nx, ny, int(p.Z))
			if s.seen[q] || !same(e.Map.Tile(q)) {
				continue
			}
			s.add(q)
			queue = append(queue, q)
		}
	}
	return s
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// strokeReplace remembers which ground an Alt stroke replaces.
type strokeReplace struct {
	stroke int64
	brush  *GroundBrush
	empty  bool
}

func (e *Editor) drawArea(tx *Tx, r DrawRequest) error {
	area := e.area(r)
	if len(area.list) == 0 {
		return nil
	}
	switch r.Kind {
	case KindRaw:
		return e.drawRaw(tx, r, area)
	case KindGround:
		return e.drawGround(tx, r, area)
	case KindEraser:
		e.drawEraser(tx, r, area)
	case KindWall:
		return e.drawWalls(tx, r, area)
	case KindCarpet, KindTable:
		return e.drawCarpetTable(tx, r, area)
	case KindHouse:
		return e.drawHouse(tx, r, area)
	case KindFlag:
		for _, p := range area.list {
			cur := tx.Get(p)
			if cur == nil || cur.Ground.ID == 0 {
				continue
			}
			has := cur.Flags&r.Flag == r.Flag
			if has == !r.Erase {
				continue
			}
			t := tx.Edit(p)
			if r.Erase {
				t.Flags &^= r.Flag
			} else {
				t.Flags |= r.Flag
			}
		}
	case KindZone:
		for _, p := range area.list {
			cur := tx.Get(p)
			if cur == nil || cur.Ground.ID == 0 {
				continue
			}
			has := slices.Contains(cur.Zones(), r.Zone)
			if has == !r.Erase {
				continue
			}
			t := tx.Edit(p)
			ext := t.EnsureExt()
			if r.Erase {
				ext.Zones = slices.DeleteFunc(ext.Zones, func(z uint16) bool { return z == r.Zone })
				t.Compact()
			} else {
				ext.Zones = append(ext.Zones, r.Zone)
			}
		}
	case KindOptionalBorder:
		for _, p := range area.list {
			cur := tx.Get(p)
			if r.Erase {
				if cur == nil || !e.hasOptionalBorder(cur) {
					continue
				}
				tx.Edit(p).State &^= otmap.StateOptionalBorder
			} else {
				if g := e.groundBrushOf(cur); g != nil && g.optional != nil {
					continue // no gravel on a mountain
				}
				tx.Edit(p).State |= otmap.StateOptionalBorder
			}
			e.borderize(tx, p)
		}
	default:
		return fmt.Errorf("%w: %s", ErrNoBrush, r.Kind)
	}
	return nil
}

func (e *Editor) drawRaw(tx *Tx, r DrawRequest, area *posSet) error {
	typ := e.itemType(r.Item)
	if typ == nil || r.Item == 0 {
		return fmt.Errorf("%w: item %d", ErrNoBrush, r.Item)
	}
	for _, p := range area.list {
		if r.Erase {
			cur := tx.Get(p)
			if cur == nil {
				continue
			}
			if cur.Ground.ID != r.Item && !slices.ContainsFunc(cur.Items, func(it otmap.Item) bool { return it.ID == r.Item }) {
				continue
			}
			t := tx.Edit(p)
			if t.Ground.ID == r.Item {
				t.Ground = otmap.Item{}
			}
			t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool { return it.ID == r.Item })
			continue
		}
		t := tx.Edit(p)
		if e.Settings.RawReplaceBottom && typ.TopOrder == 2 {
			t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool { return e.topOrder(it.ID) == 2 })
		}
		e.addItem(t, e.newItem(r.Item))
	}
	return nil
}

func (e *Editor) drawGround(tx *Tx, r DrawRequest, area *posSet) error {
	g := e.Brushes.Ground(r.Name)
	if g == nil {
		return fmt.Errorf("%w: ground %q", ErrNoBrush, r.Name)
	}
	if r.Alt && !r.Erase && e.replace.stroke != r.Stroke && len(r.Points) > 0 {
		start := e.Map.Tile(r.Points[0])
		e.replace = strokeReplace{stroke: r.Stroke, brush: e.groundBrushOf(start), empty: e.groundBrushOf(start) == nil}
	}
	for _, p := range area.list {
		cur := tx.Get(p)
		if r.Erase {
			if cur == nil || e.groundBrushOf(cur) != g {
				continue
			}
			t := tx.Edit(p)
			t.Ground = otmap.Item{}
			continue
		}
		if r.Alt {
			other := e.groundBrushOf(cur)
			if e.replace.empty {
				if other != nil {
					continue
				}
			} else if other != e.replace.brush {
				continue
			}
		}
		if len(g.items.ids) == 0 {
			continue
		}
		t := tx.Edit(p)
		if e.Settings.Automagic {
			e.cleanBorders(t)
		}
		t.Ground = e.newItem(g.items.pick(e.rng))
	}
	if e.Settings.Automagic {
		for _, p := range area.ring().list {
			e.borderize(tx, p)
		}
	}
	return nil
}

func (e *Editor) drawEraser(tx *Tx, r DrawRequest, area *posSet) {
	for _, p := range area.list {
		cur := tx.Get(p)
		if cur == nil || (cur.Ground.ID == 0 && len(cur.Items) == 0) {
			continue
		}
		t := tx.Edit(p)
		leave := e.Settings.EraserLeaveUnique
		t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool {
			if leave && (it.Complex() || (!r.Erase && e.Brushes.isBorder(it.ID))) {
				return false
			}
			return true
		})
		if r.Erase && t.Ground.ID != 0 && !(leave && t.Ground.Complex()) {
			t.Ground = otmap.Item{}
		}
	}
	if e.Settings.Automagic {
		for _, p := range area.ring().list {
			e.borderize(tx, p)
			e.wallize(tx, p)
			e.tableize(tx, p)
			e.carpetize(tx, p)
		}
	}
}

func (e *Editor) drawWalls(tx *Tx, r DrawRequest, area *posSet) error {
	w := e.Brushes.Wall(r.Name)
	if w == nil {
		return fmt.Errorf("%w: wall %q", ErrNoBrush, r.Name)
	}
	if r.Alt && r.Size == 0 && r.Rect == nil && !r.Erase {
		// Alt-click changes the shape of the wall under the cursor.
		for _, p := range area.list {
			if tx.Get(p) != nil {
				e.cycleWall(tx.Edit(p), w)
			}
		}
		return nil
	}
	for _, p := range area.list {
		cur := tx.Get(p)
		if r.Erase {
			if cur == nil || !e.hasWall(cur) {
				continue
			}
			e.cleanWalls(tx.Edit(p), w)
			continue
		}
		e.drawWall(tx.Edit(p), w)
	}
	if e.Settings.Automagic && !w.decoration {
		for _, p := range area.ring().list {
			e.wallize(tx, p)
		}
	}
	return nil
}

func (e *Editor) drawCarpetTable(tx *Tx, r DrawRequest, area *posSet) error {
	carpet := r.Kind == KindCarpet
	c, tb := e.Brushes.Carpet(r.Name), e.Brushes.Table(r.Name)
	if (carpet && c == nil) || (!carpet && tb == nil) {
		return fmt.Errorf("%w: %s %q", ErrNoBrush, r.Kind, r.Name)
	}
	for _, p := range area.list {
		cur := tx.Get(p)
		if r.Erase {
			if cur == nil || (carpet && !e.hasCarpet(cur)) || (!carpet && !e.hasTable(cur)) {
				continue
			}
			t := tx.Edit(p)
			if carpet {
				e.removeCarpets(t)
			} else {
				e.removeTables(t)
			}
			continue
		}
		t := tx.Edit(p)
		if carpet {
			e.drawCarpet(t, c)
		} else {
			e.drawTable(t, tb)
		}
	}
	for _, p := range area.ring().list {
		if carpet {
			e.carpetize(tx, p)
		} else {
			e.tableize(tx, p)
		}
	}
	return nil
}

func (e *Editor) drawDoorRequest(tx *Tx, r DrawRequest) {
	dt := slices.Index(materials.DoorTypes, r.DoorType) + 1
	if dt <= 0 {
		dt = 2 // normal
	}
	for _, p := range r.Points {
		if r.Erase {
			e.removeDoor(tx, p)
		} else {
			e.drawDoor(tx, p, dt, r.Alt)
		}
	}
	if e.Settings.Automagic {
		s := newPosSet()
		for _, p := range r.Points {
			s.add(p)
		}
		for _, p := range s.ring().list {
			e.wallize(tx, p)
		}
	}
}

func (e *Editor) drawDoodadRequest(tx *Tx, r DrawRequest) error {
	d := e.Brushes.Doodad(r.Name)
	if d == nil {
		return fmt.Errorf("%w: doodad %q", ErrNoBrush, r.Name)
	}
	if r.Erase {
		for _, p := range e.area(r).list {
			if cur := tx.Get(p); cur != nil {
				before := len(cur.Items)
				g := cur.Ground.ID
				probe := cur.Clone()
				e.eraseDoodad(probe, d)
				if len(probe.Items) != before || probe.Ground.ID != g {
					e.eraseDoodad(tx.Edit(p), d)
				}
			}
		}
		return nil
	}
	buf := e.doodad
	if buf == nil || !strings.EqualFold(buf.Brush, r.Name) || buf.Size != r.Size || buf.Shape != r.Shape || buf.Variation != r.Variation {
		e.GenerateDoodad(r.Name, r.Variation, r.Size, r.Shape, r.Thickness)
	}
	for _, p := range r.Points {
		e.placeDoodad(tx, p, r.Alt, r.Thickness)
	}
	return nil
}

func (e *Editor) drawHouse(tx *Tx, r DrawRequest, area *posSet) error {
	if r.House == 0 || (!r.Erase && e.Map.Houses[r.House] == nil) {
		return fmt.Errorf("%w: house %d", ErrNoBrush, r.House)
	}
	for _, p := range area.list {
		cur := tx.Get(p)
		if cur == nil || cur.Ground.ID == 0 {
			continue
		}
		if r.Erase {
			if cur.HouseID == 0 || cur.HouseID != r.House {
				continue
			}
			t := tx.Edit(p)
			t.HouseID = 0
			t.Flags &^= otmap.FlagPZ
			for i := range t.Items {
				if t.Items[i].Ext != nil && t.Items[i].Ext.DoorID != 0 {
					t.Items[i].Ext.DoorID = 0
					t.Items[i].Compact()
				}
			}
			continue
		}
		if cur.HouseID == r.House {
			continue
		}
		t := tx.Edit(p)
		t.HouseID = r.House
		t.Flags |= otmap.FlagPZ
		if e.Settings.HouseRemoveItems {
			t.Items = slices.DeleteFunc(t.Items, func(it otmap.Item) bool {
				typ := e.itemType(it.ID)
				return typ != nil && typ.Flags.Has(otitems.FlagMoveable) && !it.Complex()
			})
		}
		if e.Settings.AutoDoorID {
			for i := range t.Items {
				if typ := e.itemType(t.Items[i].ID); typ != nil && typ.IsDoor() {
					e.assignDoorID(tx, t, i)
				}
			}
		}
	}
	return nil
}

// assignDoorID gives a door on a house tile the lowest free door id of the
// house.
func (e *Editor) assignDoorID(tx *Tx, t *otmap.Tile, i int) {
	if t.HouseID == 0 {
		return
	}
	if t.Items[i].Ext != nil && t.Items[i].Ext.DoorID != 0 {
		return
	}
	used := map[uint8]bool{}
	for _, c := range e.Map.Chunks() {
		c.Each(func(_ otmap.Position, ot *otmap.Tile) {
			if ot.HouseID != t.HouseID {
				return
			}
			for _, it := range ot.Items {
				if it.Ext != nil && it.Ext.DoorID != 0 {
					used[it.Ext.DoorID] = true
				}
			}
		})
	}
	for id := 1; id < 256; id++ {
		if !used[uint8(id)] {
			t.Items[i].Ensure().DoorID = uint8(id)
			return
		}
	}
}

func (e *Editor) drawMarker(tx *Tx, r DrawRequest) error {
	p := r.Points[len(r.Points)-1]
	switch r.Kind {
	case KindHouseExit:
		h := e.Map.Houses[r.House]
		if h == nil {
			return fmt.Errorf("%w: house %d", ErrNoBrush, r.House)
		}
		if r.Erase {
			if h.Exit != p {
				return nil
			}
			tx.Meta()
			h.Exit = otmap.Position{}
			return nil
		}
		cur := tx.Get(p)
		if cur == nil || cur.Ground.ID == 0 || cur.HouseID != 0 || e.blocking(cur) {
			return errors.New("a house exit must be on a walkable tile outside the house")
		}
		tx.Meta()
		h.Exit = p
	case KindWaypoint:
		if r.Erase {
			for i, w := range e.Map.Waypoints {
				if w.Pos == p {
					tx.Meta()
					e.Map.Waypoints = slices.Delete(e.Map.Waypoints, i, i+1)
					return nil
				}
			}
			return nil
		}
		if r.Waypoint == "" {
			return errors.New("choose a waypoint")
		}
		tx.Meta()
		if w := e.Map.Waypoint(r.Waypoint); w != nil {
			w.Pos = p
		} else {
			e.Map.Waypoints = append(e.Map.Waypoints, &otmap.Waypoint{Name: r.Waypoint, Pos: p})
		}
	}
	return nil
}

func (e *Editor) drawSpawn(tx *Tx, r DrawRequest) {
	npc := r.Kind == KindSpawnNPC
	for _, p := range r.Points {
		cur := tx.Get(p)
		if r.Erase {
			if cur == nil || (npc && cur.SpawnNPC() == nil) || (!npc && cur.Spawn() == nil) {
				continue
			}
			t := tx.Edit(p)
			if npc {
				t.Ext.SpawnNPC = nil
			} else {
				t.Ext.Spawn = nil
			}
			t.Compact()
			continue
		}
		t := tx.Edit(p)
		s := &otmap.Spawn{Radius: int32(max(r.Size, 1))}
		if npc {
			t.EnsureExt().SpawnNPC = s
		} else {
			t.EnsureExt().Spawn = s
		}
	}
}

// maxSpawnRadius bounds the search for a spawn covering a tile.
const maxSpawnRadius = 30

// covered reports whether a spawn area covers p.
func (e *Editor) covered(p otmap.Position, npc bool) bool {
	x, y := int(p.X), int(p.Y)
	found := false
	e.Map.EachIn(x-maxSpawnRadius, y-maxSpawnRadius, x+maxSpawnRadius, y+maxSpawnRadius, int(p.Z), int(p.Z), func(q otmap.Position, t *otmap.Tile) {
		if found {
			return
		}
		s := t.Spawn()
		if npc && e.Map.SpawnFormat == otmap.SpawnsSeparate {
			s = t.SpawnNPC()
		} else if s == nil {
			s = t.SpawnNPC()
		}
		if s != nil && abs(int(q.X)-x) <= int(s.Radius) && abs(int(q.Y)-y) <= int(s.Radius) {
			found = true
		}
	})
	return found
}

func (e *Editor) drawCreature(tx *Tx, r DrawRequest) error {
	if !r.Erase && r.Creature == "" {
		return errors.New("choose a creature")
	}
	for _, p := range r.Points {
		cur := tx.Get(p)
		if r.Erase {
			if cur == nil || len(cur.Creatures()) == 0 {
				continue
			}
			t := tx.Edit(p)
			t.Ext.Creatures = nil
			t.Compact()
			continue
		}
		if cur == nil || cur.Ground.ID == 0 || (e.blocking(cur) && !r.Alt) {
			continue
		}
		t := tx.Edit(p)
		spawnTime := r.SpawnTime
		if spawnTime <= 0 {
			spawnTime = e.Settings.DefaultSpawnTime
		}
		c := otmap.Creature{Name: r.Creature, NPC: r.NPC, SpawnTime: spawnTime, Direction: r.Direction}
		t.EnsureExt().Creatures = []otmap.Creature{c}
		if e.Settings.AutoCreateSpawn && !e.covered(p, r.NPC) {
			if r.NPC && e.Map.SpawnFormat == otmap.SpawnsSeparate {
				t.Ext.SpawnNPC = &otmap.Spawn{Radius: 1}
			} else {
				t.Ext.Spawn = &otmap.Spawn{Radius: 1}
			}
		}
	}
	return nil
}
