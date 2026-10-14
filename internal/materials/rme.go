package materials

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// node is a generic XML element.
type node struct {
	name     string
	attrs    map[string]string
	children []*node
}

func parseXML(data []byte) (*node, error) {
	// RME's parser accepts NUL bytes (some data files end with one).
	data = bytes.ReplaceAll(data, []byte{0}, nil)
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: strings.ToLower(t.Name.Local), attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			if len(stack) > 0 {
				stack[len(stack)-1].children = append(stack[len(stack)-1].children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 && root != nil {
				// Whatever follows the root element is ignored, like RME does.
				return root, nil
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("empty document")
	}
	return root, nil
}

func (n *node) str(key string) string { return n.attrs[key] }

func (n *node) has(key string) bool {
	_, ok := n.attrs[key]
	return ok
}

func (n *node) num(key string) int {
	v, err := strconv.Atoi(strings.TrimSpace(n.attrs[key]))
	if err != nil {
		f, err := strconv.ParseFloat(strings.TrimSpace(n.attrs[key]), 64)
		if err != nil {
			return 0
		}
		return int(f)
	}
	return v
}

func (n *node) u16(key string) uint16 {
	v := n.num(key)
	if v < 0 || v > math.MaxUint16 {
		return 0
	}
	return uint16(v)
}

func (n *node) bool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(n.attrs[key])) {
	case "true", "yes", "1":
		return true
	}
	return false
}

// ClientToServer converts a client id (RME "lookid") to a server id.
type ClientToServer func(clientID uint16) (uint16, bool)

type importer struct {
	m        *Materials
	warnings []string
	visited  map[string]bool
	toServer ClientToServer
}

func (im *importer) warn(format string, args ...any) {
	if len(im.warnings) < 200 {
		im.warnings = append(im.warnings, fmt.Sprintf(format, args...))
	}
}

// FindRMEData returns the folder with a materials.xml inside dir: dir itself,
// or the version folder of an RME "data" folder closest to (and not newer
// than) clientVersion (e.g. 1098).
func FindRMEData(dir string, clientVersion int) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "materials.xml")); err == nil {
		return dir, nil
	}
	for _, sub := range []string{"data", filepath.Join("data", "materials")} {
		if _, err := os.Stat(filepath.Join(dir, sub, "materials.xml")); err == nil {
			return filepath.Join(dir, sub), nil
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	best, bestV := "", -1
	lowest, lowestV := "", math.MaxInt
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "materials.xml")); err != nil {
			continue
		}
		// Folder names are 3-5 digits (740, 1098, 10100): compare on the
		// same scale as the client version.
		cmpV := v
		for cmpV > 9999 {
			cmpV /= 10
		}
		if cmpV <= clientVersion && cmpV > bestV {
			best, bestV = e.Name(), cmpV
		}
		if cmpV < lowestV {
			lowest, lowestV = e.Name(), cmpV
		}
	}
	if best == "" {
		best = lowest
	}
	if best == "" {
		return "", fmt.Errorf("no materials.xml found in %s", dir)
	}
	return filepath.Join(dir, best), nil
}

// ImportRME reads a Remere's Map Editor data folder (materials.xml with its
// includes and creatures.xml).
func ImportRME(dir string, toServer ClientToServer) (*Materials, []string, error) {
	im := &importer{m: New(), visited: map[string]bool{}, toServer: toServer}
	if err := im.file(filepath.Join(dir, "materials.xml")); err != nil {
		return nil, nil, err
	}
	if data, err := os.ReadFile(filepath.Join(dir, "creatures.xml")); err == nil {
		creatures, err := ParseRMECreatures(data)
		if err != nil {
			im.warn("creatures.xml: %v", err)
		}
		im.m.Creatures = append(im.m.Creatures, creatures...)
	}
	// Brushes declared but never defined (forward declarations of another
	// version) are useless.
	im.m.Brushes = slices.DeleteFunc(im.m.Brushes, func(b *Brush) bool {
		return b.Type == "" || (b.Type == TypeGround && len(b.Items) == 0 && len(b.Alternatives) == 0)
	})
	return im.m, im.warnings, nil
}

func (im *importer) file(path string) error {
	abs, _ := filepath.Abs(path)
	if im.visited[abs] {
		return nil
	}
	im.visited[abs] = true
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	root, err := parseXML(data)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	for _, n := range root.children {
		switch n.name {
		case "include":
			f := n.str("file")
			if f == "" {
				continue
			}
			if err := im.file(filepath.Join(filepath.Dir(path), f)); err != nil {
				im.warn("include %s: %v", f, err)
			}
		case "metaitem":
			from, to := idRange(n)
			for id := from; id <= to && id != 0; id++ {
				im.m.MetaItems = append(im.m.MetaItems, uint16(id))
			}
		case "border":
			b := borderNode(n)
			if b.ID == 0 {
				im.warn("border without an id")
				continue
			}
			if cur := im.m.Border(b.ID); cur != nil {
				*cur = *b
			} else {
				im.m.Borders = append(im.m.Borders, b)
			}
		case "brush":
			im.brush(n)
		case "tileset":
			im.tileset(n)
		}
	}
	return nil
}

func idRange(n *node) (uint32, uint32) {
	if n.has("id") {
		v := uint32(n.u16("id"))
		return v, v
	}
	a, b := uint32(n.u16("fromid")), uint32(n.u16("toid"))
	if b < a {
		b = a
	}
	return a, b
}

func borderNode(n *node) *Border {
	b := &Border{ID: uint32(n.num("id")), Group: n.u16("group"), Edges: map[string]uint16{}}
	if strings.EqualFold(n.str("type"), "optional") {
		b.Optional = true
	}
	for _, c := range n.children {
		edge := strings.ToLower(c.str("edge"))
		id := c.u16("item")
		if id != 0 && slices.Contains(EdgeNames, edge) {
			b.Edges[edge] = id
		}
	}
	return b
}

func (im *importer) look(n *node) uint16 {
	if n.has("server_lookid") {
		return n.u16("server_lookid")
	}
	if n.has("lookid") {
		id := n.u16("lookid")
		if im.toServer != nil {
			if s, ok := im.toServer(id); ok {
				return s
			}
		}
		return id
	}
	return 0
}

func itemSpec(n *node) ItemSpec {
	s := ItemSpec{ID: n.u16("id")}
	for _, k := range []string{"subtype", "count"} {
		if n.has(k) {
			s.Subtype = n.u16(k)
		}
	}
	for _, k := range []string{"aid", "actionid"} {
		if n.has(k) {
			s.ActionID = n.u16(k)
		}
	}
	for _, k := range []string{"uid", "uniqueid"} {
		if n.has(k) {
			s.UniqueID = n.u16(k)
		}
	}
	s.Text = n.str("text")
	return s
}

func (im *importer) brush(n *node) {
	name := n.str("name")
	if name == "" {
		im.warn("brush without a name")
		return
	}
	typ := strings.ToLower(n.str("type"))
	switch typ {
	case "wall decoration":
		typ = TypeDecoration
	case "ground", "wall", "doodad", "carpet", "table", "":
	default:
		im.warn("brush %q has unknown type %q", name, typ)
		return
	}
	b := im.m.Brush(name)
	if b == nil {
		b = &Brush{Name: name}
		im.m.Brushes = append(im.m.Brushes, b)
	}
	if typ != "" {
		b.Type = typ
	}
	if l := im.look(n); l != 0 {
		b.Look = l
	}
	if len(n.children) == 0 {
		return
	}
	switch b.Type {
	case TypeGround:
		im.ground(b, n)
	case TypeWall, TypeDecoration:
		im.wall(b, n)
	case TypeDoodad:
		im.doodad(b, n)
	case TypeCarpet, TypeTable:
		im.parts(b, n)
	}
}

func (im *importer) ground(b *Brush, n *node) {
	if n.has("z-order") {
		b.ZOrder = n.num("z-order")
	}
	b.SoloOptional = n.bool("solo_optional")
	if n.has("randomize") && !n.bool("randomize") {
		b.NoRandomize = true
	}
	for _, c := range n.children {
		switch c.name {
		case "item":
			b.Items = append(b.Items, Chance{ID: c.u16("id"), Chance: c.num("chance")})
		case "optional":
			r := &BorderRule{Align: "outer"}
			if c.has("ground_equivalent") {
				inl := borderNode(c)
				inl.ID = 0
				inl.GroundEquivalent = c.u16("ground_equivalent")
				r.Inline = inl
			} else {
				r.Border = uint32(c.num("id"))
			}
			b.Optional = r
		case "border":
			r := BorderRule{Align: "outer", To: "all"}
			if strings.EqualFold(c.str("align"), "inner") {
				r.Align = "inner"
			}
			if c.has("to") {
				r.To = c.str("to")
			}
			r.Super = c.bool("super")
			if c.has("id") {
				r.Border = uint32(c.num("id"))
			} else if c.has("ground_equivalent") {
				inl := borderNode(c)
				inl.ID = 0
				inl.GroundEquivalent = c.u16("ground_equivalent")
				r.Inline = inl
			}
			for _, sc := range c.children {
				if sc.name == "specific" {
					r.Specific = append(r.Specific, specific(sc))
				}
			}
			b.Borders = append(b.Borders, r)
		case "friend":
			b.Friends = append(b.Friends, c.str("name"))
			b.HateFriends = false
		case "enemy":
			b.Friends = append(b.Friends, c.str("name"))
			b.HateFriends = true
		case "clear_borders":
			b.Borders = nil
		case "clear_friends":
			b.Friends = nil
			b.HateFriends = false
		}
	}
}

func specific(n *node) SpecificCase {
	var sc SpecificCase
	for _, part := range n.children {
		for _, c := range part.children {
			switch part.name {
			case "conditions":
				switch c.name {
				case "match_border":
					sc.Conditions = append(sc.Conditions, Condition{Kind: "border", Border: uint32(c.num("id")), Edge: strings.ToLower(c.str("edge"))})
				case "match_group":
					sc.Conditions = append(sc.Conditions, Condition{Kind: "group", Group: c.u16("group"), Edge: strings.ToLower(c.str("edge"))})
				case "match_item":
					sc.Conditions = append(sc.Conditions, Condition{Kind: "item", Item: c.u16("id")})
				}
			case "actions":
				switch c.name {
				case "replace_border":
					sc.Actions = append(sc.Actions, Action{Kind: "replaceBorder", Border: uint32(c.num("id")), Edge: strings.ToLower(c.str("edge")), With: c.u16("with")})
				case "replace_item":
					sc.Actions = append(sc.Actions, Action{Kind: "replaceItem", Item: c.u16("id"), With: c.u16("with")})
				case "delete_borders":
					sc.Actions = append(sc.Actions, Action{Kind: "deleteBorders"})
				}
			}
		}
	}
	return sc
}

func (im *importer) wall(b *Brush, n *node) {
	if b.Walls == nil {
		b.Walls = map[string]*WallPart{}
	}
	for _, c := range n.children {
		switch c.name {
		case "wall":
			align := c.str("type")
			if !slices.Contains(WallAlignments, align) {
				align = wallAlias(align)
				if align == "" {
					im.warn("wall brush %q: unknown alignment %q", b.Name, c.str("type"))
					continue
				}
			}
			part := b.Walls[align]
			if part == nil {
				part = &WallPart{}
				b.Walls[align] = part
			}
			for _, it := range c.children {
				switch it.name {
				case "item":
					part.Items = append(part.Items, Chance{ID: it.u16("id"), Chance: it.num("chance")})
				case "door":
					typ := strings.ReplaceAll(strings.ToLower(it.str("type")), "_", " ")
					open := true
					if it.has("open") {
						open = it.bool("open")
					}
					d := Door{ID: it.u16("id"), Open: open, Hate: it.bool("hate")}
					switch typ {
					case "any door":
						for _, t := range []string{"archway", "normal", "locked", "quest", "magic"} {
							d.Type = t
							part.Doors = append(part.Doors, d)
						}
					case "any window":
						for _, t := range []string{"window", "hatch window"} {
							d.Type = t
							part.Doors = append(part.Doors, d)
						}
					case "any":
						for _, t := range DoorTypes {
							d.Type = t
							part.Doors = append(part.Doors, d)
						}
					default:
						if !slices.Contains(DoorTypes, typ) {
							im.warn("wall brush %q: unknown door type %q", b.Name, typ)
							continue
						}
						d.Type = typ
						part.Doors = append(part.Doors, d)
					}
				}
			}
		case "friend":
			b.Friends = append(b.Friends, c.str("name"))
			if c.bool("redirect") {
				b.Redirect = c.str("name")
			}
		case "alternate", "composite", "item":
			// Some wall brushes are placed like doodads.
			im.doodad(b, n)
			return
		}
	}
}

func wallAlias(s string) string {
	switch strings.ToLower(s) {
	case "northwest diagonal":
		return "corner"
	case "south t":
		return "south T"
	case "east t":
		return "east T"
	case "west t":
		return "west T"
	case "north t":
		return "north T"
	}
	for _, a := range WallAlignments {
		if strings.EqualFold(a, s) {
			return a
		}
	}
	return ""
}

func (im *importer) doodad(b *Brush, n *node) {
	o := b.Doodad
	if o == nil {
		o = &DoodadOptions{}
		b.Doodad = o
	}
	o.OnBlocking = n.bool("on_blocking")
	o.OnDuplicate = n.bool("on_duplicate")
	o.RedoBorders = n.bool("redo_borders") || n.bool("reborder")
	o.OneSize = n.bool("one_size")
	o.Draggable = n.bool("draggable")
	o.RemoveOptionalBorder = n.bool("remove_optional_border")
	if t := n.str("thickness"); t != "" {
		if a, c, ok := strings.Cut(t, "/"); ok {
			o.Thickness, _ = strconv.Atoi(strings.TrimSpace(a))
			o.ThicknessCeiling, _ = strconv.Atoi(strings.TrimSpace(c))
		}
	}
	var top Alternative
	for _, c := range n.children {
		if c.name == "alternate" {
			b.Alternatives = append(b.Alternatives, alternative(c))
		}
	}
	top = alternative(n)
	if len(top.Singles) > 0 || len(top.Composites) > 0 {
		if len(b.Alternatives) == 0 {
			b.Alternatives = append(b.Alternatives, top)
		} else {
			last := &b.Alternatives[len(b.Alternatives)-1]
			last.Singles = append(last.Singles, top.Singles...)
			last.Composites = append(last.Composites, top.Composites...)
		}
	}
}

func alternative(n *node) Alternative {
	var a Alternative
	for _, c := range n.children {
		switch c.name {
		case "item":
			a.Singles = append(a.Singles, Single{Chance: c.num("chance"), Item: itemSpec(c)})
		case "composite":
			comp := Composite{Chance: c.num("chance")}
			for _, t := range c.children {
				if t.name != "tile" {
					continue
				}
				ct := CompositeTile{X: t.num("x"), Y: t.num("y"), Z: t.num("z")}
				for _, it := range t.children {
					if it.name == "item" {
						ct.Items = append(ct.Items, itemSpec(it))
					}
				}
				if len(ct.Items) > 0 {
					comp.Tiles = append(comp.Tiles, ct)
				}
			}
			if len(comp.Tiles) > 0 {
				a.Composites = append(a.Composites, comp)
			}
		}
	}
	return a
}

var tableAligns = map[string]string{
	"north": "north", "south": "south", "east": "east", "west": "west",
	"horizontal": "horizontal", "vertical": "vertical", "alone": "alone",
}

func (im *importer) parts(b *Brush, n *node) {
	if b.Parts == nil {
		b.Parts = map[string][]Chance{}
	}
	for _, c := range n.children {
		if c.name != "carpet" && c.name != "table" {
			continue
		}
		align := strings.ToLower(c.str("align"))
		if b.Type == TypeCarpet {
			if align == "center" {
				align = "c"
			}
			if align != "c" && !slices.Contains(EdgeNames, align) {
				im.warn("carpet %q: unknown alignment %q", b.Name, align)
				continue
			}
		} else if tableAligns[align] == "" {
			im.warn("table %q: unknown alignment %q", b.Name, align)
			continue
		}
		if c.has("id") {
			b.Parts[align] = append(b.Parts[align], Chance{ID: c.u16("id"), Chance: max(c.num("chance"), 1)})
		}
		for _, it := range c.children {
			if it.name == "item" {
				b.Parts[align] = append(b.Parts[align], Chance{ID: it.u16("id"), Chance: it.num("chance")})
			}
		}
	}
}

func (im *importer) tileset(n *node) {
	name := n.str("name")
	if name == "" {
		return
	}
	t := im.m.Tileset(name)
	if t == nil {
		t = &Tileset{Name: name}
		im.m.Tilesets = append(im.m.Tilesets, t)
	}
	for _, cat := range n.children {
		raw := strings.HasSuffix(cat.name, "_and_raw") || cat.name == "raw"
		base := strings.TrimSuffix(cat.name, "_and_raw")
		for _, c := range cat.children {
			switch c.name {
			case "brush":
				bn := c.str("name")
				if bn == "" {
					continue
				}
				if base == "doodad" {
					t.Doodads = appendMissing(t.Doodads, []string{bn})
				} else {
					t.Terrain = appendMissing(t.Terrain, []string{bn})
				}
			case "item":
				from, to := idRange(c)
				for id := from; id <= to && id != 0; id++ {
					switch {
					case base == "items" || base == "collections":
						t.Items = appendMissingIDs(t.Items, []uint16{uint16(id)})
						if raw {
							t.Raw = appendMissingIDs(t.Raw, []uint16{uint16(id)})
						}
					default:
						t.Raw = appendMissingIDs(t.Raw, []uint16{uint16(id)})
					}
				}
			}
		}
	}
}

// ParseRMECreatures reads an RME creatures.xml.
func ParseRMECreatures(data []byte) ([]*CreatureType, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	var out []*CreatureType
	for _, n := range root.children {
		if n.name != "creature" || n.str("name") == "" {
			continue
		}
		c := &CreatureType{Name: n.str("name"), NPC: strings.EqualFold(n.str("type"), "npc")}
		c.Outfit = Outfit{
			LookType: n.u16("looktype"), Head: uint8(n.num("lookhead")), Body: uint8(n.num("lookbody")),
			Legs: uint8(n.num("looklegs")), Feet: uint8(n.num("lookfeet")), Addons: uint8(n.num("lookaddon")),
			Mount: n.u16("lookmount"),
		}
		// lookitem is a server id in RME.
		c.Outfit.LookItem = n.u16("lookitem")
		out = append(out, c)
	}
	return out, nil
}
