package materials

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nekiro/ots-creator/internal/otitems"
)

// maxScanFiles limits how many files a creature scan reads.
const maxScanFiles = 50000

var (
	reMonsterType = regexp.MustCompile(`Game\.createMonsterType\(\s*"([^"]+)"`)
	reNpcType     = regexp.MustCompile(`Game\.createNpcType\(\s*"([^"]+)"`)
	reNpcVar      = regexp.MustCompile(`(?:internalNpcName|npcName)\s*=\s*"([^"]+)"`)
	reNpcConfig   = regexp.MustCompile(`npcConfig\.name\s*=\s*"([^"]+)"`)
	reOutfit      = regexp.MustCompile(`(?s)\.outfit\s*=\s*\{(.*?)\}`)
	reKeyValue    = regexp.MustCompile(`(\w+)\s*=\s*(\d+)`)
)

// ScanCreatures finds monsters and NPCs in a server data folder: TFS XML
// (monster/*.xml, npc/*.xml) and Lua scripts (revscripts).
func ScanCreatures(dir string) ([]*CreatureType, error) {
	var out []*CreatureType
	seen := map[string]bool{}
	add := func(c *CreatureType) {
		key := strings.ToLower(c.Name)
		if c.Name == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, c)
	}
	files := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped
		}
		if d.IsDir() {
			name := strings.ToLower(d.Name())
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "world" || name == "logs") {
				return filepath.SkipDir
			}
			return nil
		}
		files++
		if files > maxScanFiles {
			return filepath.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".xml" && ext != ".lua" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if ext == ".xml" {
			if c := xmlCreature(data); c != nil {
				add(c)
			}
			return nil
		}
		for _, c := range luaCreatures(string(data)) {
			add(c)
		}
		return nil
	})
	slices.SortFunc(out, func(a, b *CreatureType) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, err
}

func xmlCreature(data []byte) *CreatureType {
	// Cheap check before parsing every xml file of a server.
	head := strings.ToLower(string(data[:min(len(data), 512)]))
	if !strings.Contains(head, "<monster") && !strings.Contains(head, "<npc") {
		return nil
	}
	root, err := parseXML(data)
	if err != nil || (root.name != "monster" && root.name != "npc") || root.str("name") == "" {
		return nil
	}
	c := &CreatureType{Name: root.str("name"), NPC: root.name == "npc"}
	for _, n := range root.children {
		if n.name != "look" {
			continue
		}
		c.Outfit = Outfit{
			LookType: n.u16("type"), Head: uint8(n.num("head")), Body: uint8(n.num("body")),
			Legs: uint8(n.num("legs")), Feet: uint8(n.num("feet")), Addons: uint8(n.num("addons")),
			Mount: n.u16("mount"), LookItem: n.u16("typeex"),
		}
	}
	return c
}

func luaCreatures(src string) []*CreatureType {
	var out []*CreatureType
	outfit := func() Outfit {
		var o Outfit
		m := reOutfit.FindStringSubmatch(src)
		if m == nil {
			return o
		}
		for _, kv := range reKeyValue.FindAllStringSubmatch(m[1], -1) {
			v, _ := strconv.Atoi(kv[2])
			switch strings.ToLower(kv[1]) {
			case "looktype":
				o.LookType = uint16(v)
			case "lookhead":
				o.Head = uint8(v)
			case "lookbody":
				o.Body = uint8(v)
			case "looklegs":
				o.Legs = uint8(v)
			case "lookfeet":
				o.Feet = uint8(v)
			case "lookaddons":
				o.Addons = uint8(v)
			case "lookmount":
				o.Mount = uint16(v)
			case "looktypeex":
				o.LookItem = uint16(v)
			}
		}
		return o
	}
	if m := reMonsterType.FindStringSubmatch(src); m != nil {
		out = append(out, &CreatureType{Name: m[1], Outfit: outfit()})
		return out
	}
	name := ""
	for _, re := range []*regexp.Regexp{reNpcType, reNpcVar, reNpcConfig} {
		if m := re.FindStringSubmatch(src); m != nil {
			name = m[1]
			break
		}
	}
	if name != "" && (strings.Contains(src, "createNpcType") || strings.Contains(src, "npcConfig")) {
		out = append(out, &CreatureType{Name: name, NPC: true, Outfit: outfit()})
	}
	return out
}

// AutoTilesets groups every item by its flags, so a client without brush
// definitions still has an organized palette.
func AutoTilesets(db *otitems.DB) []*Tileset {
	groups := []struct {
		name string
		test func(t *otitems.Type) bool
	}{
		{"Grounds", func(t *otitems.Type) bool { return t.IsGround() }},
		{"Borders", func(t *otitems.Type) bool { return t.TopOrder == 1 }},
		{"Walls", func(t *otitems.Type) bool { return t.TopOrder == 2 && !t.IsDoor() }},
		{"Doors", func(t *otitems.Type) bool { return t.IsDoor() }},
		{"Stairs & holes", func(t *otitems.Type) bool { return t.Flags.Has(otitems.FlagFloorChange) || t.IsTeleport() }},
		{"Containers", func(t *otitems.Type) bool { return t.IsContainer() }},
		{"Fluids & splashes", func(t *otitems.Type) bool {
			return t.Flags&(otitems.FlagSplash|otitems.FlagFluidContainer) != 0
		}},
		{"Top items", func(t *otitems.Type) bool { return t.TopOrder == 3 }},
		{"Light sources", func(t *otitems.Type) bool {
			return t.Flags.Has(otitems.FlagLight) && !t.Flags.Has(otitems.FlagPickupable)
		}},
		{"Blocking decoration", func(t *otitems.Type) bool {
			return t.Flags.Has(otitems.FlagUnpassable) && !t.Flags.Has(otitems.FlagPickupable)
		}},
		{"Decoration", func(t *otitems.Type) bool { return !t.Flags.Has(otitems.FlagPickupable) }},
		{"Pickupable items", func(t *otitems.Type) bool { return t.Flags.Has(otitems.FlagPickupable) }},
	}
	sets := make([]*Tileset, len(groups))
	for i, g := range groups {
		sets[i] = &Tileset{Name: g.name, Auto: true}
	}
	db.Each(func(t *otitems.Type) {
		if t.Flags.Has(otitems.FlagMeta) || t.ClientID == 0 {
			return
		}
		for i, g := range groups {
			if g.test(t) {
				sets[i].Raw = append(sets[i].Raw, t.ID)
				return
			}
		}
	})
	return slices.DeleteFunc(sets, func(s *Tileset) bool { return len(s.Raw) == 0 })
}
