package mapedit

import (
	"slices"
	"testing"

	"github.com/nekiro/ots-creator/internal/materials"
	"github.com/nekiro/ots-creator/internal/otitems"
	"github.com/nekiro/ots-creator/internal/otmap"
)

// Item ids of the test materials.
const (
	grassA, grassB        = 100, 101
	sandID                = 110
	borderN, borderE      = 200, 201
	borderS, borderW      = 202, 203
	borderCNW, borderCNE  = 204, 205
	borderCSW, borderCSE  = 206, 207
	borderDNW, borderDNE  = 208, 209
	borderDSE, borderDSW  = 210, 211
	wallH, wallV, wallC   = 300, 301, 302
	wallPoleID            = 303
	doorHClosed, doorHOpn = 310, 311
	rugC, rugN            = 400, 401
	tableAloneID, tableH  = 500, 501
	tableWestID, tableEID = 502, 503
	treeID                = 600
	stoneID               = 700
	bagID                 = 800
	coinID                = 801
)

func testEditor(t *testing.T) *Editor {
	t.Helper()
	var types []*otitems.Type
	add := func(id uint16, f otitems.Flags, top uint8) {
		types = append(types, &otitems.Type{ID: id, ClientID: id, Flags: f, TopOrder: top, Name: "item"})
	}
	for _, id := range []uint16{grassA, grassB, sandID} {
		add(id, otitems.FlagGround, 0)
	}
	for id := uint16(borderN); id <= borderDSW; id++ {
		add(id, 0, 1)
	}
	for _, id := range []uint16{wallH, wallV, wallC, wallPoleID, doorHClosed, doorHOpn} {
		add(id, otitems.FlagUnpassable, 2)
	}
	for _, id := range []uint16{rugC, rugN, tableAloneID, tableH, tableWestID, tableEID, treeID} {
		add(id, otitems.FlagUnpassable, 0)
	}
	add(stoneID, otitems.FlagMoveable|otitems.FlagPickupable, 0)
	add(bagID, otitems.FlagContainer|otitems.FlagMoveable, 0)
	add(coinID, otitems.FlagStackable|otitems.FlagMoveable, 0)
	db := otitems.NewDB(otitems.ModeOTB, types)
	types[len(types)-2].Kind = otitems.KindContainer
	db.Get(doorHClosed).Kind = otitems.KindDoor
	db.Get(doorHOpn).Kind = otitems.KindDoor

	m := materials.New()
	m.Borders = []*materials.Border{{ID: 1, Edges: map[string]uint16{
		"n": borderN, "e": borderE, "s": borderS, "w": borderW, "cnw": borderCNW, "cne": borderCNE,
		"csw": borderCSW, "cse": borderCSE, "dnw": borderDNW, "dne": borderDNE, "dse": borderDSE, "dsw": borderDSW,
	}}}
	m.Brushes = []*materials.Brush{
		{Name: "grass", Type: materials.TypeGround, ZOrder: 10, Items: []materials.Chance{{ID: grassA, Chance: 10}, {ID: grassB, Chance: 10}},
			Borders: []materials.BorderRule{{Align: "outer", Border: 1}}},
		{Name: "sand", Type: materials.TypeGround, ZOrder: 5, Items: []materials.Chance{{ID: sandID, Chance: 1}}},
		{Name: "stone wall", Type: materials.TypeWall, Walls: map[string]*materials.WallPart{
			"horizontal": {Items: []materials.Chance{{ID: wallH, Chance: 1}}, Doors: []materials.Door{{ID: doorHClosed, Type: "normal"}, {ID: doorHOpn, Type: "normal", Open: true}}},
			"vertical":   {Items: []materials.Chance{{ID: wallV, Chance: 1}}},
			"corner":     {Items: []materials.Chance{{ID: wallC, Chance: 1}}},
			"pole":       {Items: []materials.Chance{{ID: wallPoleID, Chance: 1}}},
		}},
		{Name: "rug", Type: materials.TypeCarpet, Parts: map[string][]materials.Chance{"c": {{ID: rugC, Chance: 1}}, "n": {{ID: rugN, Chance: 1}}}},
		{Name: "desk", Type: materials.TypeTable, Parts: map[string][]materials.Chance{
			"alone": {{ID: tableAloneID, Chance: 1}}, "horizontal": {{ID: tableH, Chance: 1}},
			"west": {{ID: tableWestID, Chance: 1}}, "east": {{ID: tableEID, Chance: 1}},
		}},
		{Name: "trees", Type: materials.TypeDoodad, Doodad: &materials.DoodadOptions{Draggable: true, Thickness: 100, ThicknessCeiling: 100},
			Alternatives: []materials.Alternative{{Singles: []materials.Single{{Chance: 1, Item: materials.ItemSpec{ID: treeID}}}}}},
		// A stamp made from a selection: sand with a stone, and a lone tree.
		{Name: "pit", Type: materials.TypeDoodad, Doodad: &materials.DoodadOptions{OnBlocking: true, OneSize: true},
			Alternatives: []materials.Alternative{{Composites: []materials.Composite{{Chance: 1, Tiles: []materials.CompositeTile{
				{X: 0, Y: 0, Items: []materials.ItemSpec{{ID: sandID}, {ID: stoneID}, {ID: borderN}}},
				{X: 1, Y: 0, Items: []materials.ItemSpec{{ID: treeID}}},
			}}}}}},
	}
	mp := otmap.New(1000, 1000)
	e := New(mp, db, NewBrushes(db, m))
	e.Seed(1)
	return e
}

func at(x, y int) otmap.Position { return pos(x, y, 7) }

func itemIDs(t *otmap.Tile) []uint16 {
	if t == nil {
		return nil
	}
	var ids []uint16
	for _, it := range t.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestBorderPiecesMatchesShapes(t *testing.T) {
	cases := map[int][]int{
		0:                         nil,
		bitN:                      {edgeN},
		bitN | bitNW | bitNE:      {edgeN},
		bitN | bitW:               {edgeDNW},
		bitN | bitW | bitE:        {edgeN, edgeE, edgeW},
		bitNE | bitSW:             {edgeCNE, edgeCSW},
		bitS | bitE | bitNW:       {edgeDSE, edgeCNW},
		bitN | bitE | bitS | bitW: {edgeN, edgeE, edgeS, edgeW},
	}
	for mask, want := range cases {
		got := borderPieces(mask)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("mask %08b: got %v want %v", mask, got, want)
		}
	}
}

func TestGroundBrushBorders(t *testing.T) {
	e := testEditor(t)
	// Paint sand, then a grass square in the middle.
	if _, err := e.Draw(DrawRequest{Kind: KindGround, Name: "sand", Points: []otmap.Position{at(50, 50)}, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Draw(DrawRequest{Kind: KindGround, Name: "grass", Points: []otmap.Position{at(50, 50)}, Size: 0}); err != nil {
		t.Fatal(err)
	}
	g := e.Map.Tile(at(50, 50))
	if g.Ground.ID != grassA && g.Ground.ID != grassB {
		t.Fatalf("ground = %d", g.Ground.ID)
	}
	// The outer border of grass lies on the sand around it.
	want := map[[2]int]uint16{
		{50, 49}: borderS, {50, 51}: borderN, {49, 50}: borderE, {51, 50}: borderW,
		{49, 49}: borderCSE, {51, 49}: borderCSW, {49, 51}: borderCNE, {51, 51}: borderCNW,
	}
	for xy, id := range want {
		got := itemIDs(e.Map.Tile(at(xy[0], xy[1])))
		if !slices.Equal(got, []uint16{id}) {
			t.Errorf("tile %v items %v, want [%d]", xy, got, id)
		}
	}
	// Painting the neighbor turns the border into a diagonal shape later.
	e.Draw(DrawRequest{Kind: KindGround, Name: "grass", Points: []otmap.Position{at(51, 50)}})
	if got := itemIDs(e.Map.Tile(at(51, 50))); len(got) != 0 {
		t.Errorf("grass tile kept border %v", got)
	}
	if got := itemIDs(e.Map.Tile(at(50, 49))); !slices.Equal(got, []uint16{borderS}) {
		t.Errorf("north of grass: %v", got)
	}
	// Erasing the grass removes the borders.
	e.Draw(DrawRequest{Kind: KindGround, Name: "grass", Points: []otmap.Position{at(50, 50), at(51, 50)}, Erase: true})
	for xy := range want {
		if got := itemIDs(e.Map.Tile(at(xy[0], xy[1]))); len(got) != 0 {
			t.Errorf("border left at %v: %v", xy, got)
		}
	}
}

func TestUndoRedoAndStrokeMerge(t *testing.T) {
	e := testEditor(t)
	for i := 0; i < 3; i++ {
		e.Draw(DrawRequest{Kind: KindRaw, Item: stoneID, Points: []otmap.Position{at(10+i, 10)}, Stroke: 7})
	}
	if u, _ := e.History(); len(u) != 1 || u[0].Tiles != 3 {
		t.Fatalf("history = %+v", u)
	}
	if !e.Changed() {
		t.Fatal("not changed")
	}
	e.Undo()
	for i := 0; i < 3; i++ {
		if e.Map.Tile(at(10+i, 10)) != nil {
			t.Fatal("undo left a tile")
		}
	}
	if e.Changed() {
		t.Fatal("changed after full undo")
	}
	e.Redo()
	if got := itemIDs(e.Map.Tile(at(12, 10))); !slices.Equal(got, []uint16{stoneID}) {
		t.Fatalf("redo = %v", got)
	}
	e.MarkSaved()
	if e.Changed() {
		t.Fatal("changed after save")
	}
	dirty, _ := e.TakeDirty()
	if len(dirty) == 0 {
		t.Fatal("no dirty chunks")
	}
}

func TestWalls(t *testing.T) {
	e := testEditor(t)
	// A horizontal line of three walls.
	for x := 20; x <= 22; x++ {
		e.Draw(DrawRequest{Kind: KindWall, Name: "stone wall", Points: []otmap.Position{at(x, 20)}})
	}
	for x := 20; x <= 22; x++ {
		got := itemIDs(e.Map.Tile(at(x, 20)))
		want := wallH
		if x == 20 {
			// west end of a reduced set falls back to the pole... the
			// reduced set keeps only N/W: the west end has no west
			// neighbor, so it is a pole.
			want = wallPoleID
		}
		if !slices.Equal(got, []uint16{uint16(want)}) {
			t.Errorf("x=%d: %v want %d", x, got, want)
		}
	}
	// A wall going south from the corner turns it into a corner piece.
	e.Draw(DrawRequest{Kind: KindWall, Name: "stone wall", Points: []otmap.Position{at(22, 21)}})
	if got := itemIDs(e.Map.Tile(at(22, 21))); !slices.Equal(got, []uint16{wallV}) {
		t.Errorf("south wall: %v", got)
	}
	// Door on the middle wall, then open it.
	e.Draw(DrawRequest{Kind: KindDoor, DoorType: "normal", Points: []otmap.Position{at(21, 20)}})
	if got := itemIDs(e.Map.Tile(at(21, 20))); !slices.Equal(got, []uint16{doorHClosed}) {
		t.Fatalf("door: %v", got)
	}
	if err := e.SwitchDoor(at(21, 20), 0); err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(e.Map.Tile(at(21, 20))); !slices.Equal(got, []uint16{doorHOpn}) {
		t.Fatalf("open door: %v", got)
	}
	// Erasing the door turns it back into a wall.
	e.Draw(DrawRequest{Kind: KindDoor, Erase: true, Points: []otmap.Position{at(21, 20)}})
	if got := itemIDs(e.Map.Tile(at(21, 20))); !slices.Equal(got, []uint16{wallH}) {
		t.Fatalf("door removed: %v", got)
	}
}

func TestCarpetAndTable(t *testing.T) {
	e := testEditor(t)
	e.Draw(DrawRequest{Kind: KindCarpet, Name: "rug", Points: []otmap.Position{at(30, 30)}, Size: 1})
	if got := itemIDs(e.Map.Tile(at(30, 30))); !slices.Equal(got, []uint16{rugC}) {
		t.Errorf("rug center: %v", got)
	}
	// The south row continues north, east and west: piece "n" (named by
	// the middle side the carpet continues to).
	if got := itemIDs(e.Map.Tile(at(30, 31))); !slices.Equal(got, []uint16{rugN}) {
		t.Errorf("rug south row: %v", got)
	}
	e.Draw(DrawRequest{Kind: KindTable, Name: "desk", Points: []otmap.Position{at(40, 40)}})
	if got := itemIDs(e.Map.Tile(at(40, 40))); !slices.Equal(got, []uint16{tableAloneID}) {
		t.Fatalf("alone table: %v", got)
	}
	e.Draw(DrawRequest{Kind: KindTable, Name: "desk", Points: []otmap.Position{at(41, 40)}})
	if got := itemIDs(e.Map.Tile(at(40, 40))); !slices.Equal(got, []uint16{tableWestID}) {
		t.Errorf("west end: %v", got)
	}
	if got := itemIDs(e.Map.Tile(at(41, 40))); !slices.Equal(got, []uint16{tableEID}) {
		t.Errorf("east end: %v", got)
	}
}

func TestDoodadStampReplacesTiles(t *testing.T) {
	e := testEditor(t)
	e.Draw(DrawRequest{Kind: KindGround, Name: "grass", Points: []otmap.Position{at(60, 60)}, Size: 2})
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(60, 60)}})
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(61, 60)}})
	e.GenerateDoodad("pit", 0, 0, "square", 0)
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "pit", Points: []otmap.Position{at(60, 60)}})
	// The tile with ground is replaced whole, even with a tree on it, and keeps
	// its stack as copied (no reordering of the border).
	if got := e.Map.Tile(at(60, 60)); got == nil || got.Ground.ID != sandID || !slices.Equal(itemIDs(got), []uint16{stoneID, borderN}) {
		t.Fatalf("stamped tile: %+v", got)
	}
	// A tile without ground adds on top and keeps the ground there.
	if got := e.Map.Tile(at(61, 60)); got == nil || got.Ground.ID == 0 || !slices.Contains(itemIDs(got), treeID) {
		t.Fatalf("tile without ground: %+v", got)
	}
}

func TestDoodadOnBlocking(t *testing.T) {
	e := testEditor(t)
	e.Draw(DrawRequest{Kind: KindGround, Name: "sand", Points: []otmap.Position{at(60, 60)}, Size: 2})
	buf := e.GenerateDoodad("trees", 0, 0, "square", 0)
	if len(buf.Tiles) != 1 || buf.Tiles[0].Items[0].ID != treeID {
		t.Fatalf("buffer = %+v", buf)
	}
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(60, 60)}})
	if got := itemIDs(e.Map.Tile(at(60, 60))); !slices.Equal(got, []uint16{treeID}) {
		t.Fatalf("tree: %v", got)
	}
	// A tree makes the tile blocking: no second tree without Alt.
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(60, 60)}})
	if got := itemIDs(e.Map.Tile(at(60, 60))); len(got) != 1 {
		t.Fatalf("second tree placed: %v", got)
	}
	// No tree on an empty tile.
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(90, 90)}})
	if e.Map.Tile(at(90, 90)) != nil {
		t.Fatal("tree placed in the void")
	}
	// A big brush scatters several trees.
	e.Draw(DrawRequest{Kind: KindGround, Name: "sand", Points: []otmap.Position{at(200, 200)}, Size: 5})
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(200, 200)}, Size: 4})
	n := 0
	e.Map.EachIn(190, 190, 210, 210, 7, 7, func(_ otmap.Position, t *otmap.Tile) {
		if slices.Contains(itemIDs(t), treeID) {
			n++
		}
	})
	if n < 3 {
		t.Fatalf("only %d trees", n)
	}
	// Erasing removes them.
	e.Draw(DrawRequest{Kind: KindDoodad, Name: "trees", Points: []otmap.Position{at(200, 200)}, Size: 6, Erase: true})
	e.Map.EachIn(190, 190, 210, 210, 7, 7, func(_ otmap.Position, t *otmap.Tile) {
		if slices.Contains(itemIDs(t), treeID) {
			n = -100
		}
	})
	if n < 0 {
		t.Fatal("erase left trees")
	}
}

func TestSelectionMoveCopyPaste(t *testing.T) {
	e := testEditor(t)
	e.Settings.Automagic = false
	e.Draw(DrawRequest{Kind: KindGround, Name: "sand", Points: []otmap.Position{at(70, 70)}, Size: 1})
	e.Draw(DrawRequest{Kind: KindRaw, Item: stoneID, Points: []otmap.Position{at(70, 70)}})
	e.SelectRect(Rect{From: at(69, 69), To: at(71, 71)}, FloorsCurrent, false, false, true)
	if e.SelectionCount() != 9 {
		t.Fatalf("selected %d", e.SelectionCount())
	}
	if err := e.MoveSelection(10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if e.Map.Tile(at(70, 70)) != nil {
		t.Fatal("source not emptied")
	}
	moved := e.Map.Tile(at(80, 70))
	if moved == nil || moved.Ground.ID != sandID || !slices.Equal(itemIDs(moved), []uint16{stoneID}) || !moved.Selected() {
		t.Fatalf("moved = %+v", moved)
	}
	clip := e.Copy()
	if clip == nil || len(clip.Tiles) != 9 || clip.Width != 3 {
		t.Fatalf("clip = %+v", clip)
	}
	if err := e.Paste(clip, at(100, 100)); err != nil {
		t.Fatal(err)
	}
	if p := e.Map.Tile(at(101, 101)); p == nil || !slices.Equal(itemIDs(p), []uint16{stoneID}) {
		t.Fatal("paste")
	}
	if e.Map.Tile(at(80, 70)).Selected() {
		t.Fatal("old selection kept after paste")
	}
	if n := e.DeleteSelection(""); n != 9 {
		t.Fatalf("deleted %d", n)
	}
	e.Undo()
	if e.Map.Tile(at(101, 101)) == nil {
		t.Fatal("undo delete")
	}
	// Click selection picks the top item.
	if what := e.SelectAt(at(80, 70), false, true, true); what != "item" {
		t.Fatalf("SelectAt = %q", what)
	}
	tl := e.Map.Tile(at(80, 70))
	if tl.Ground.Selected || !tl.Items[0].Selected || e.SelectionCount() != 1 {
		t.Fatal("click selection wrong")
	}
}

func TestToolsAndInspector(t *testing.T) {
	e := testEditor(t)
	e.Draw(DrawRequest{Kind: KindGround, Name: "sand", Points: []otmap.Position{at(5, 5)}})
	e.Draw(DrawRequest{Kind: KindRaw, Item: bagID, Points: []otmap.Position{at(5, 5)}})
	ref := ItemRef{Pos: at(5, 5), Index: 0}
	if err := e.AddToContainer(ref, coinID); err != nil {
		t.Fatal(err)
	}
	aid := uint16(1234)
	if err := e.UpdateItem(ItemRef{Pos: at(5, 5), Index: 0, Path: []int{0}}, ItemPatch{ActionID: &aid}); err != nil {
		t.Fatal(err)
	}
	res, _ := e.Find(FindQuery{Kind: "action", Containers: true})
	if len(res) != 1 || res[0].ID != coinID || res[0].Detail != "aid 1234" {
		t.Fatalf("find = %+v", res)
	}
	if res, _ := e.Find(FindQuery{IDs: []uint16{coinID}}); len(res) != 0 {
		t.Fatal("found container content without Containers")
	}
	if n := e.ReplaceItems(map[uint16]uint16{coinID: stoneID}, false); n != 1 {
		t.Fatalf("replaced %d", n)
	}
	if got := e.Map.Tile(at(5, 5)).Items[0].Ext.Items[0]; got.ID != stoneID || got.ActionID() != 1234 {
		t.Fatalf("replaced item = %+v", got)
	}
	s := e.Stats()
	if s.Tiles != 1 || s.Items != 3 || s.Containers != 1 || s.ContainerItems != 1 || s.ActionIDs != 1 {
		t.Fatalf("stats = %+v", s)
	}
	if err := e.RemoveItem(ItemRef{Pos: at(5, 5), Index: 0}); err != nil {
		t.Fatal(err)
	}
	if len(e.Map.Tile(at(5, 5)).Items) != 0 {
		t.Fatal("item not removed")
	}
	// Remove unreachable: a lone blocking tile far away goes.
	e.Draw(DrawRequest{Kind: KindRaw, Item: treeID, Points: []otmap.Position{at(500, 500)}})
	if n := e.RemoveUnreachable(); n != 1 {
		t.Fatalf("unreachable removed %d", n)
	}
}

func TestHouseAndCreatures(t *testing.T) {
	e := testEditor(t)
	e.Draw(DrawRequest{Kind: KindGround, Name: "sand", Points: []otmap.Position{at(10, 10)}, Size: 2})
	e.Draw(DrawRequest{Kind: KindRaw, Item: stoneID, Points: []otmap.Position{at(10, 10)}})
	id, err := e.SaveHouse(otmap.House{Name: "Villa", TownID: 1})
	if err != nil || id != 1 {
		t.Fatal(id, err)
	}
	e.Draw(DrawRequest{Kind: KindHouse, House: id, Points: []otmap.Position{at(10, 10)}, Size: 1})
	tl := e.Map.Tile(at(10, 10))
	if tl.HouseID != 1 || tl.Flags&otmap.FlagPZ == 0 || len(tl.Items) != 0 {
		t.Fatalf("house tile = %+v", tl)
	}
	if hs := e.Houses(); len(hs) != 1 || hs[0].Size != 9 {
		t.Fatalf("houses = %+v", hs)
	}
	if _, err := e.Draw(DrawRequest{Kind: KindHouseExit, House: id, Points: []otmap.Position{at(10, 10)}}); err == nil {
		t.Fatal("exit inside the house accepted")
	}
	if _, err := e.Draw(DrawRequest{Kind: KindHouseExit, House: id, Points: []otmap.Position{at(12, 12)}}); err != nil {
		t.Fatal(err)
	}
	if e.Map.Houses[1].Exit != at(12, 12) {
		t.Fatal("exit not set")
	}
	e.Undo()
	if e.Map.Houses[1].Exit != (otmap.Position{}) {
		t.Fatal("exit undo")
	}
	e.Draw(DrawRequest{Kind: KindCreature, Creature: "Rat", Points: []otmap.Position{at(12, 12)}})
	ct := e.Map.Tile(at(12, 12))
	if len(ct.Creatures()) != 1 || ct.Spawn() == nil || ct.Creatures()[0].SpawnTime != 60 {
		t.Fatalf("creature tile = %+v", ct.Ext)
	}
	e.Draw(DrawRequest{Kind: KindCreature, Creature: "Rat", Points: []otmap.Position{at(11, 12)}})
	if e.Map.Tile(at(11, 12)).Spawn() != nil {
		t.Fatal("second spawn created inside the first")
	}
	if n := e.RemoveEmptySpawns(); n != 0 {
		t.Fatalf("removed %d spawns", n)
	}
	e.RemoveHouse(1)
	if e.Map.Tile(at(10, 10)).HouseID != 0 {
		t.Fatal("house tiles kept")
	}
}
