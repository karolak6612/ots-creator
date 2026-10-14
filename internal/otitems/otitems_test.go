package otitems

import (
	"bytes"
	"errors"
	"testing"

	"github.com/nekiro/ots-creator/internal/otbnode"
	"github.com/nekiro/ots-creator/internal/thing"
)

// writeOTB builds a minimal items.otb with the given items.
func writeOTB(t *testing.T, items []struct {
	group          Group
	flags          uint32
	server, client uint16
	top            uint8
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := otbnode.NewWriter(&buf, [4]byte{})
	w.Start(0)
	w.U32(0)
	w.U8(0x01)
	w.U16(140)
	w.U32(3)
	w.U32(57)
	w.U32(9)
	w.Bytes(make([]byte, 128))
	for _, it := range items {
		w.Start(byte(it.group))
		w.U32(it.flags)
		w.U8(otbAttrServerID)
		w.U16(2)
		w.U16(it.server)
		w.U8(otbAttrClientID)
		w.U16(2)
		w.U16(it.client)
		if it.top != 0 {
			w.U8(otbAttrTopOrder)
			w.U16(1)
			w.U8(it.top)
		}
		w.U8(0x99) // unknown attribute is skipped
		w.U16(3)
		w.Bytes([]byte{1, 2, 3})
		w.End()
	}
	w.End()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseOTB(t *testing.T) {
	data := writeOTB(t, []struct {
		group          Group
		flags          uint32
		server, client uint16
		top            uint8
	}{
		{GroupGround, 0, 100, 100, 0},
		{GroupNone, otbAlwaysOnTop | otbUnpassable, 1050, 1060, 2},
		{GroupContainer, otbPickupable | otbMoveable, 1987, 1987, 0},
		{GroupNone, otbStackable | otbPickupable, 2148, 3031, 0},
	})
	db, err := ParseOTB(data)
	if err != nil {
		t.Fatal(err)
	}
	if db.MajorVersion != 3 || db.MinorVersion != 57 || db.Mode != ModeOTB {
		t.Fatalf("header %d.%d mode %s", db.MajorVersion, db.MinorVersion, db.Mode)
	}
	if g := db.Get(100); g == nil || !g.IsGround() {
		t.Fatal("ground missing")
	}
	w := db.Get(1050)
	if w == nil || w.ClientID != 1060 || w.TopOrder != 2 || !w.Flags.Has(FlagUnpassable) {
		t.Fatalf("wall = %+v", w)
	}
	if !db.Get(1987).IsContainer() || !db.Get(2148).HasSubtype() {
		t.Fatal("container or stackable flags lost")
	}
	if id, ok := db.FromClient(3031); !ok || id != 2148 {
		t.Fatalf("FromClient = %d %v", id, ok)
	}

	xmlData := []byte(`<?xml version="1.0"?><items>
		<item id="100" name="void"/>
		<item fromid="1050" toid="1051" article="a" name="stone wall"><attribute key="rotateTo" value="1051"/></item>
		<item id="1987" name="bag"><attribute key="type" value="container"/><attribute key="containerSize" value="8"/></item>
	</items>`)
	if err := db.ApplyXML(xmlData); err != nil {
		t.Fatal(err)
	}
	if db.Get(100).Name != "void" || db.Get(1050).Name != "stone wall" || db.Get(1050).RotateTo != 1051 {
		t.Fatal("xml names not applied")
	}
	if b := db.Get(1987); b.Kind != KindContainer || b.MaxItems != 8 {
		t.Fatalf("bag = %+v", b)
	}
	if got := db.Search("wall", 10); len(got) != 1 || got[0] != 1050 {
		t.Fatalf("search = %v", got)
	}
	if err := db.ApplyXML([]byte(`<other/>`)); !errors.Is(err, ErrNoItems) {
		t.Fatalf("err = %v", err)
	}
}

type fakeSource map[uint32]*thing.Thing

func (f fakeSource) Thing(c thing.Category, id uint32) (*thing.Thing, error) {
	if t, ok := f[id]; ok {
		return t, nil
	}
	return nil, errors.New("missing")
}

func TestFromClient(t *testing.T) {
	ground := thing.New(100, thing.CategoryItem)
	ground.Props.Ground = true
	ground.Props.GroundSpeed = 150
	ground.Props.MiniMap = true
	ground.Props.MiniMapColor = 24
	border := thing.New(101, thing.CategoryItem)
	border.Props.GroundBorder = true
	top := thing.New(102, thing.CategoryItem)
	top.Props.OnTop = true
	top.Name = "arch"
	db := FromClient(fakeSource{100: ground, 101: border, 102: top}, 103, map[uint32]string{100: "grass"})
	if db.Mode != ModeClient {
		t.Fatal("mode")
	}
	g := db.Get(100)
	if !g.IsGround() || g.Speed != 150 || g.MinimapColor != 24 || g.Name != "grass" || g.ClientID != 100 {
		t.Fatalf("ground = %+v", g)
	}
	if !db.Get(101).IsBorder() || db.Get(102).TopOrder != 3 || db.Get(102).Name != "arch" {
		t.Fatal("top orders or names wrong")
	}
	if db.Get(103) != nil {
		t.Fatal("missing client item created")
	}
}
