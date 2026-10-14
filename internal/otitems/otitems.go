// Package otitems is the item type database of the map editor. Maps store
// server ids: with an items.otb (TFS and older servers) they map to client
// ids through it; without one (servers that use client ids
// directly) both ids are the same. Flags come from the otb when present and
// from the client (dat or appearances) otherwise; names come from items.xml
// or the client.
package otitems

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/nekiro/ots-creator/internal/otbnode"
	"github.com/nekiro/ots-creator/internal/thing"
)

// Mode tells how map item ids relate to client ids.
type Mode string

const (
	// ModeOTB maps server ids to client ids through items.otb.
	ModeOTB Mode = "otb"
	// ModeClient uses client ids as server ids (no items.otb).
	ModeClient Mode = "client"
)

// Group is the item group stored in items.otb.
type Group uint8

const (
	GroupNone Group = iota
	GroupGround
	GroupContainer
	GroupWeapon
	GroupAmmunition
	GroupArmor
	GroupCharges
	GroupTeleport
	GroupMagicField
	GroupWritable
	GroupKey
	GroupSplash
	GroupFluid
	GroupDoor
	GroupDeprecated
)

// Kind is the item type declared in items.xml ("type" attribute).
type Kind uint8

const (
	KindNone Kind = iota
	KindDepot
	KindMailbox
	KindTrashHolder
	KindContainer
	KindDoor
	KindMagicField
	KindTeleport
	KindBed
	KindKey
	KindPodium
	KindRune
)

var kindNames = map[string]Kind{
	"depot": KindDepot, "mailbox": KindMailbox, "trashholder": KindTrashHolder, "container": KindContainer,
	"door": KindDoor, "magicfield": KindMagicField, "teleport": KindTeleport, "bed": KindBed, "key": KindKey,
	"podium": KindPodium, "rune": KindRune,
}

// String returns the items.xml name of the kind.
func (k Kind) String() string {
	for n, v := range kindNames {
		if v == k {
			return n
		}
	}
	return ""
}

// Flags are item properties relevant to editing and drawing.
type Flags uint32

const (
	FlagGround Flags = 1 << iota
	FlagUnpassable
	FlagBlockMissile
	FlagBlockPath
	FlagPickupable
	FlagMoveable
	FlagStackable
	FlagFluidContainer
	FlagSplash
	FlagHangable
	FlagHookSouth
	FlagHookEast
	FlagRotatable
	FlagReadable
	FlagWritable
	FlagContainer
	FlagFloorChange
	FlagElevation
	FlagFullGround
	FlagCharges
	FlagCorpse
	// FlagMeta marks editor-only items that are never saved.
	FlagMeta
	FlagIgnoreLook
	FlagLight
)

// Has reports whether all bits of f are set.
func (f Flags) Has(x Flags) bool { return f&x == x }

// Type is one item type.
type Type struct {
	ID       uint16 `json:"id"`
	ClientID uint16 `json:"clientId"`
	Name     string `json:"name"`
	Article  string `json:"article,omitempty"`
	Plural   string `json:"plural,omitempty"`
	Group    Group  `json:"group"`
	Kind     Kind   `json:"kind"`
	// TopOrder is 1 for borders, 2 for bottom items (walls) and 3 for top
	// items (always drawn above creatures); 0 for common items.
	TopOrder     uint8  `json:"topOrder"`
	Flags        Flags  `json:"flags"`
	MinimapColor uint16 `json:"minimapColor"`
	Speed        uint16 `json:"speed"`
	RotateTo     uint16 `json:"rotateTo,omitempty"`
	MaxItems     uint16 `json:"maxItems,omitempty"`
	LightLevel   uint8  `json:"lightLevel,omitempty"`
	LightColor   uint8  `json:"lightColor,omitempty"`
	EditorSuffix string `json:"editorSuffix,omitempty"`
}

// IsGround reports whether the item is a ground tile.
func (t *Type) IsGround() bool { return t.Flags.Has(FlagGround) }

// IsBorder reports whether the item is a ground border.
func (t *Type) IsBorder() bool { return t.TopOrder == 1 }

// HasSubtype reports whether the item stores a count or fluid subtype.
func (t *Type) HasSubtype() bool {
	return t.Flags&(FlagStackable|FlagFluidContainer|FlagSplash|FlagCharges) != 0
}

// IsContainer reports whether the item can hold other items.
func (t *Type) IsContainer() bool {
	return t.Flags.Has(FlagContainer) || t.Kind == KindContainer || t.Group == GroupContainer
}

// IsDoor reports whether the item is a door.
func (t *Type) IsDoor() bool { return t.Kind == KindDoor || t.Group == GroupDoor }

// IsTeleport reports whether the item has a teleport destination.
func (t *Type) IsTeleport() bool { return t.Kind == KindTeleport || t.Group == GroupTeleport }

// IsDepot reports whether the item is a depot locker.
func (t *Type) IsDepot() bool { return t.Kind == KindDepot }

// DisplayName returns the name or a placeholder.
func (t *Type) DisplayName() string {
	if t.Name != "" {
		return t.Name
	}
	return "item " + strconv.Itoa(int(t.ID))
}

// DB is the item database. It is read-only after loading.
type DB struct {
	Mode Mode `json:"mode"`
	// OTB versions, written into OTBM headers.
	MajorVersion uint32 `json:"majorVersion"`
	MinorVersion uint32 `json:"minorVersion"`
	BuildNumber  uint32 `json:"buildNumber"`
	items        []*Type
	byClient     map[uint16]uint16
}

// Get returns the type of a server id, or nil.
func (db *DB) Get(id uint16) *Type {
	if db == nil || int(id) >= len(db.items) {
		return nil
	}
	return db.items[id]
}

// MaxID returns the highest server id.
func (db *DB) MaxID() uint16 {
	if len(db.items) == 0 {
		return 0
	}
	return uint16(len(db.items) - 1)
}

// FromClient returns the first server id that shows a client id.
func (db *DB) FromClient(clientID uint16) (uint16, bool) {
	id, ok := db.byClient[clientID]
	return id, ok
}

// Each calls fn for every item type in id order.
func (db *DB) Each(fn func(*Type)) {
	for _, t := range db.items {
		if t != nil {
			fn(t)
		}
	}
}

// Count returns the number of item types.
func (db *DB) Count() int {
	n := 0
	for _, t := range db.items {
		if t != nil {
			n++
		}
	}
	return n
}

func (db *DB) set(t *Type) {
	for int(t.ID) >= len(db.items) {
		db.items = append(db.items, nil)
	}
	db.items[t.ID] = t
}

func (db *DB) index() {
	db.byClient = make(map[uint16]uint16, len(db.items))
	for _, t := range db.items {
		if t == nil || t.ClientID == 0 {
			continue
		}
		if _, ok := db.byClient[t.ClientID]; !ok {
			db.byClient[t.ClientID] = t.ID
		}
	}
}

// otb flags (ItemEditor / RME bit layout)
const (
	otbUnpassable     = 1 << 0
	otbBlockMissiles  = 1 << 1
	otbBlockPath      = 1 << 2
	otbHasElevation   = 1 << 3
	otbPickupable     = 1 << 5
	otbMoveable       = 1 << 6
	otbStackable      = 1 << 7
	otbFloorChangeAll = 0x1F << 8
	otbAlwaysOnTop    = 1 << 13
	otbReadable       = 1 << 14
	otbRotatable      = 1 << 15
	otbHangable       = 1 << 16
	otbHookEast       = 1 << 17
	otbHookSouth      = 1 << 18
	otbClientCharges  = 1 << 22
	otbIgnoreLook     = 1 << 23
)

// otb attributes
const (
	otbAttrServerID     = 0x10
	otbAttrClientID     = 0x11
	otbAttrName         = 0x12
	otbAttrSpeed        = 0x14
	otbAttrMinimapColor = 0x21
	otbAttrLight2       = 0x2A
	otbAttrTopOrder     = 0x2B
)

// ParseOTB reads an items.otb file.
func ParseOTB(data []byte) (*DB, error) {
	root, _, err := otbnode.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("items.otb: %w", err)
	}
	db := &DB{Mode: ModeOTB}
	r := otbnode.NewReader(root.Props)
	r.U32() // flags, unused
	if r.More() {
		if attr := r.U8(); attr == 0x01 { // ROOT_ATTR_VERSION
			n := int(r.U16())
			if n >= 12 {
				db.MajorVersion, db.MinorVersion, db.BuildNumber = r.U32(), r.U32(), r.U32()
				r.Skip(n - 12)
			} else {
				r.Skip(n)
			}
		}
	}
	if r.Err() != nil {
		return nil, fmt.Errorf("items.otb header: %w", r.Err())
	}
	for _, n := range root.Children {
		group := Group(n.Type)
		if group == GroupDeprecated {
			continue
		}
		r := otbnode.NewReader(n.Props)
		flags := r.U32()
		t := &Type{Group: group}
		t.Flags = otbFlags(flags, group)
		if flags&otbAlwaysOnTop != 0 {
			t.TopOrder = 1 // refined by the top order attribute
		}
		for r.More() {
			attr := r.U8()
			size := int(r.U16())
			if r.Err() != nil {
				break
			}
			switch {
			case attr == otbAttrServerID && size == 2:
				t.ID = r.U16()
			case attr == otbAttrClientID && size == 2:
				t.ClientID = r.U16()
			case attr == otbAttrSpeed && size == 2:
				t.Speed = r.U16()
			case attr == otbAttrMinimapColor && size == 2:
				t.MinimapColor = r.U16()
			case attr == otbAttrTopOrder && size == 1:
				t.TopOrder = r.U8()
			case attr == otbAttrName:
				t.Name = string(r.Bytes(size))
			case attr == otbAttrLight2 && size == 4:
				t.LightLevel = uint8(r.U16())
				t.LightColor = uint8(r.U16())
			default:
				r.Skip(size)
			}
		}
		if r.Err() != nil {
			return nil, fmt.Errorf("items.otb item: %w", r.Err())
		}
		if flags&otbAlwaysOnTop == 0 {
			t.TopOrder = 0
		}
		if t.ID == 0 {
			continue
		}
		db.set(t)
	}
	db.index()
	return db, nil
}

func otbFlags(f uint32, g Group) Flags {
	var out Flags
	pairs := []struct {
		bit uint32
		f   Flags
	}{
		{otbUnpassable, FlagUnpassable}, {otbBlockMissiles, FlagBlockMissile}, {otbBlockPath, FlagBlockPath},
		{otbHasElevation, FlagElevation}, {otbPickupable, FlagPickupable}, {otbMoveable, FlagMoveable},
		{otbStackable, FlagStackable}, {otbReadable, FlagReadable}, {otbRotatable, FlagRotatable},
		{otbHangable, FlagHangable}, {otbHookEast, FlagHookEast}, {otbHookSouth, FlagHookSouth},
		{otbClientCharges, FlagCharges}, {otbIgnoreLook, FlagIgnoreLook},
	}
	for _, p := range pairs {
		if f&p.bit != 0 {
			out |= p.f
		}
	}
	if f&otbFloorChangeAll != 0 {
		out |= FlagFloorChange
	}
	switch g {
	case GroupGround:
		out |= FlagGround
	case GroupContainer:
		out |= FlagContainer
	case GroupSplash:
		out |= FlagSplash
	case GroupFluid:
		out |= FlagFluidContainer
	case GroupWritable:
		out |= FlagWritable | FlagReadable
	case GroupCharges:
		out |= FlagCharges
	}
	return out
}

// ThingSource gives the client items an item database reads flags from.
type ThingSource interface {
	Thing(c thing.Category, id uint32) (*thing.Thing, error)
}

// FromClient builds a database where server ids are client ids, from every
// item of the client. maxID is the highest client item id.
func FromClient(src ThingSource, maxID uint32, names map[uint32]string) *DB {
	db := &DB{Mode: ModeClient}
	for id := uint32(100); id <= maxID && id <= 0xFFFF; id++ {
		th, err := src.Thing(thing.CategoryItem, id)
		if err != nil {
			continue
		}
		t := &Type{ID: uint16(id), ClientID: uint16(id), Name: names[id]}
		applyThing(t, th, true)
		db.set(t)
	}
	db.index()
	return db
}

// ApplyClient completes an otb database with what only the client stores
// (minimap colors, full ground, light) and fills flags of items whose otb
// entry has none.
func (db *DB) ApplyClient(src ThingSource) {
	for _, t := range db.items {
		if t == nil || t.ClientID == 0 {
			continue
		}
		th, err := src.Thing(thing.CategoryItem, uint32(t.ClientID))
		if err != nil {
			continue
		}
		applyThing(t, th, false)
	}
}

// applyThing copies client flags. all also sets what items.otb normally
// provides (group, top order, blocking...).
func applyThing(t *Type, th *thing.Thing, all bool) {
	p := &th.Props
	if p.MiniMap && t.MinimapColor == 0 {
		t.MinimapColor = p.MiniMapColor
	}
	if p.FullGround {
		t.Flags |= FlagFullGround
	}
	if p.HasLight && t.LightLevel == 0 {
		t.LightLevel, t.LightColor = uint8(p.LightLevel), uint8(p.LightColor)
	}
	if p.HasLight {
		t.Flags |= FlagLight
	}
	if p.Corpse || p.PlayerCorpse {
		t.Flags |= FlagCorpse
	}
	if t.Name == "" && th.Name != "" {
		t.Name = th.Name
	}
	if !all {
		// otb clients still need these for drawing.
		if p.HookSouth {
			t.Flags |= FlagHookSouth
		}
		if p.HookEast {
			t.Flags |= FlagHookEast
		}
		if p.Hangable {
			t.Flags |= FlagHangable
		}
		return
	}
	set := func(on bool, f Flags) {
		if on {
			t.Flags |= f
		}
	}
	set(p.Ground, FlagGround)
	set(p.Unpassable, FlagUnpassable)
	set(p.BlockMissile, FlagBlockMissile)
	set(p.BlockPathfind, FlagBlockPath)
	set(p.Pickupable, FlagPickupable)
	set(!p.Unmoveable, FlagMoveable)
	set(p.Stackable, FlagStackable)
	set(p.FluidContainer, FlagFluidContainer)
	set(p.Fluid, FlagSplash)
	set(p.Hangable, FlagHangable)
	set(p.HookSouth, FlagHookSouth)
	set(p.HookEast, FlagHookEast)
	set(p.Rotatable, FlagRotatable)
	set(p.Writable || p.WritableOnce, FlagReadable)
	set(p.Writable, FlagWritable)
	set(p.Container, FlagContainer)
	set(p.FloorChange, FlagFloorChange)
	set(p.HasElevation, FlagElevation)
	set(p.HasCharges, FlagCharges)
	set(p.IgnoreLook, FlagIgnoreLook)
	switch {
	case p.GroundBorder:
		t.TopOrder = 1
	case p.OnBottom:
		t.TopOrder = 2
	case p.OnTop:
		t.TopOrder = 3
	}
	switch {
	case p.Ground:
		t.Group = GroupGround
		t.Speed = p.GroundSpeed
	case p.Container:
		t.Group = GroupContainer
	case p.Fluid:
		t.Group = GroupSplash
	case p.FluidContainer:
		t.Group = GroupFluid
	case p.Writable || p.WritableOnce:
		t.Group = GroupWritable
	}
	if p.Container {
		t.Kind = KindContainer
	}
}

// ErrNoItems is returned when items.xml has no items element.
var ErrNoItems = errors.New("items.xml: no <items> element")

// ApplyXML reads names and types from an items.xml. Ids are server ids
// (client ids in ModeClient). Ids missing from the database are skipped.
func (db *DB) ApplyXML(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	type attrNode struct {
		Key   string `xml:"key,attr"`
		Value string `xml:"value,attr"`
	}
	type itemNode struct {
		ID           string     `xml:"id,attr"`
		FromID       string     `xml:"fromid,attr"`
		ToID         string     `xml:"toid,attr"`
		Name         string     `xml:"name,attr"`
		Article      string     `xml:"article,attr"`
		Plural       string     `xml:"plural,attr"`
		EditorSuffix string     `xml:"editorsuffix,attr"`
		Attrs        []attrNode `xml:"attribute"`
	}
	found := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("items.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch strings.ToLower(se.Name.Local) {
		case "items":
			found = true
		case "item":
			var n itemNode
			if err := dec.DecodeElement(&n, &se); err != nil {
				return fmt.Errorf("items.xml: %w", err)
			}
			from, to := parseRange(n.ID, n.FromID, n.ToID)
			for id := from; id <= to && id != 0; id++ {
				t := db.Get(uint16(id))
				if t == nil {
					if id == to {
						break
					}
					continue
				}
				if n.Name != "" {
					t.Name = n.Name
				}
				if n.Article != "" {
					t.Article = n.Article
				}
				if n.Plural != "" {
					t.Plural = n.Plural
				}
				if n.EditorSuffix != "" {
					t.EditorSuffix = n.EditorSuffix
				}
				for _, a := range n.Attrs {
					applyXMLAttr(t, strings.ToLower(a.Key), a.Value)
				}
				if id == to {
					break
				}
			}
		}
	}
	if !found {
		return ErrNoItems
	}
	return nil
}

func parseRange(id, from, to string) (uint32, uint32) {
	num := func(s string) uint32 {
		n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 16)
		return uint32(n)
	}
	if id != "" {
		v := num(id)
		return v, v
	}
	a, b := num(from), num(to)
	if b < a {
		b = a
	}
	return a, b
}

func applyXMLAttr(t *Type, key, value string) {
	switch key {
	case "type":
		if k, ok := kindNames[strings.ToLower(value)]; ok {
			t.Kind = k
			if k == KindContainer || k == KindDepot || k == KindMailbox || k == KindTrashHolder {
				t.Flags |= FlagContainer
			}
		}
	case "rotateto":
		n, _ := strconv.ParseUint(value, 10, 16)
		t.RotateTo = uint16(n)
	case "containersize":
		n, _ := strconv.ParseUint(value, 10, 16)
		t.MaxItems = uint16(n)
		t.Flags |= FlagContainer
	case "charges":
		t.Flags |= FlagCharges
	case "corpsetype":
		t.Flags |= FlagCorpse
	case "writeable", "writable":
		t.Flags |= FlagWritable | FlagReadable
	case "readable":
		t.Flags |= FlagReadable
	}
}

// Search returns server ids whose name contains q (case-insensitive) or
// whose id equals q, at most limit.
func (db *DB) Search(q string, limit int) []uint16 {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []uint16
	if q == "" {
		return out
	}
	if n, err := strconv.ParseUint(q, 10, 16); err == nil {
		if t := db.Get(uint16(n)); t != nil {
			out = append(out, t.ID)
		}
	}
	for _, t := range db.items {
		if t == nil || len(out) >= limit {
			continue
		}
		if strings.Contains(strings.ToLower(t.Name), q) && !slices.Contains(out, t.ID) {
			out = append(out, t.ID)
		}
	}
	return out
}

// NewDB returns a database with the given types.
func NewDB(mode Mode, types []*Type) *DB {
	db := &DB{Mode: mode}
	for _, t := range types {
		db.set(t)
	}
	db.index()
	return db
}

// MarkMeta marks ids as editor-only items (never saved), creating types for
// ids the database does not have.
func (db *DB) MarkMeta(ids []uint16) {
	for _, id := range ids {
		t := db.Get(id)
		if t == nil {
			t = &Type{ID: id, Name: "editor item"}
			db.set(t)
		}
		t.Flags |= FlagMeta
	}
}
