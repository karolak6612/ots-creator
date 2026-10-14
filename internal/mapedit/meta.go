package mapedit

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nekiro/ots-creator/internal/otmap"
)

// Properties are the editable map header fields.
type Properties struct {
	Description string            `json:"description"`
	Width       uint16            `json:"width"`
	Height      uint16            `json:"height"`
	HouseFile   string            `json:"houseFile"`
	SpawnFile   string            `json:"spawnFile"`
	NpcFile     string            `json:"npcFile"`
	ZoneFile    string            `json:"zoneFile"`
	SpawnFormat otmap.SpawnFormat `json:"spawnFormat"`
}

// Properties returns the map header.
func (e *Editor) Properties() Properties {
	m := e.Map
	return Properties{m.Description, m.Width, m.Height, m.HouseFile, m.SpawnFile, m.NpcFile, m.ZoneFile, m.SpawnFormat}
}

// SetProperties changes the map header.
func (e *Editor) SetProperties(p Properties) error {
	if p.SpawnFormat != otmap.SpawnsSingle && p.SpawnFormat != otmap.SpawnsSeparate {
		p.SpawnFormat = otmap.SpawnsSingle
	}
	tx := e.Begin("Map properties", 0)
	tx.Meta()
	m := e.Map
	m.Description, m.Width, m.Height = p.Description, max(p.Width, 1), max(p.Height, 1)
	m.HouseFile, m.SpawnFile, m.NpcFile, m.ZoneFile, m.SpawnFormat = p.HouseFile, p.SpawnFile, p.NpcFile, p.ZoneFile, p.SpawnFormat
	tx.Commit()
	return nil
}

// HouseInfo is a house with its size.
type HouseInfo struct {
	otmap.House
	Size int `json:"size"`
}

// Houses lists the houses with their tile counts.
func (e *Editor) Houses() []HouseInfo {
	sizes := otmap.HouseSizes(e.Map)
	var out []HouseInfo
	for _, h := range e.Map.SortedHouses() {
		out = append(out, HouseInfo{*h, sizes[h.ID]})
	}
	return out
}

// SaveHouse creates or updates a house (ID 0 creates one with a free id).
func (e *Editor) SaveHouse(h otmap.House) (uint32, error) {
	if strings.TrimSpace(h.Name) == "" {
		return 0, errors.New("the house needs a name")
	}
	tx := e.Begin("Edit house", 0)
	tx.Meta()
	if h.ID == 0 {
		for id := uint32(1); ; id++ {
			if e.Map.Houses[id] == nil {
				h.ID = id
				break
			}
		}
		tx.label = "Add house"
	}
	cp := h
	e.Map.Houses[h.ID] = &cp
	tx.Commit()
	return h.ID, nil
}

// RemoveHouse deletes a house and clears its tiles.
func (e *Editor) RemoveHouse(id uint32) {
	tx := e.Begin("Remove house", 0)
	tx.Meta()
	delete(e.Map.Houses, id)
	e.Map.Each(func(p otmap.Position, t *otmap.Tile) {
		if t.HouseID == id {
			nt := tx.Edit(p)
			nt.HouseID = 0
			nt.Flags &^= otmap.FlagPZ
		}
	})
	tx.Commit()
}

// HouseTiles returns the tiles of a house.
func (e *Editor) HouseTiles(id uint32) []otmap.Position {
	var out []otmap.Position
	e.Map.Each(func(p otmap.Position, t *otmap.Tile) {
		if t.HouseID == id {
			out = append(out, p)
		}
	})
	return out
}

// SaveTown creates or updates a town (ID 0 creates one).
func (e *Editor) SaveTown(t otmap.Town) (uint32, error) {
	if strings.TrimSpace(t.Name) == "" {
		return 0, errors.New("the town needs a name")
	}
	tx := e.Begin("Edit town", 0)
	tx.Meta()
	if t.ID == 0 {
		for id := uint32(1); ; id++ {
			if e.Map.Towns[id] == nil {
				t.ID = id
				break
			}
		}
	}
	cp := t
	e.Map.Towns[t.ID] = &cp
	tx.Commit()
	return t.ID, nil
}

// RemoveTown deletes a town. Houses of the town keep their town id.
func (e *Editor) RemoveTown(id uint32) {
	tx := e.Begin("Remove town", 0)
	tx.Meta()
	delete(e.Map.Towns, id)
	tx.Commit()
}

// SaveWaypoint creates, renames or moves a waypoint. old is the current
// name ("" for a new waypoint).
func (e *Editor) SaveWaypoint(old string, w otmap.Waypoint) error {
	w.Name = strings.TrimSpace(w.Name)
	if w.Name == "" {
		return errors.New("the waypoint needs a name")
	}
	if !strings.EqualFold(old, w.Name) && e.Map.Waypoint(w.Name) != nil {
		return fmt.Errorf("a waypoint named %q exists", w.Name)
	}
	tx := e.Begin("Edit waypoint", 0)
	tx.Meta()
	if cur := e.Map.Waypoint(old); old != "" && cur != nil {
		*cur = w
	} else {
		cp := w
		e.Map.Waypoints = append(e.Map.Waypoints, &cp)
	}
	tx.Commit()
	return nil
}

// RemoveWaypoint deletes a waypoint.
func (e *Editor) RemoveWaypoint(name string) {
	tx := e.Begin("Remove waypoint", 0)
	tx.Meta()
	e.Map.Waypoints = slices.DeleteFunc(e.Map.Waypoints, func(w *otmap.Waypoint) bool { return w.Name == name })
	tx.Commit()
}

// SaveZone creates or renames a zone (ID 0 creates one).
func (e *Editor) SaveZone(z otmap.Zone) (uint16, error) {
	if strings.TrimSpace(z.Name) == "" {
		return 0, errors.New("the zone needs a name")
	}
	tx := e.Begin("Edit zone", 0)
	tx.Meta()
	if z.ID == 0 {
		used := map[uint16]bool{}
		for _, x := range e.Map.Zones {
			used[x.ID] = true
		}
		for id := uint16(1); ; id++ {
			if !used[id] {
				z.ID = id
				break
			}
		}
	}
	i := slices.IndexFunc(e.Map.Zones, func(x otmap.Zone) bool { return x.ID == z.ID })
	if i >= 0 {
		e.Map.Zones[i] = z
	} else {
		e.Map.Zones = append(e.Map.Zones, z)
	}
	tx.Commit()
	return z.ID, nil
}

// RemoveZone deletes a zone and removes it from tiles.
func (e *Editor) RemoveZone(id uint16) {
	tx := e.Begin("Remove zone", 0)
	tx.Meta()
	e.Map.Zones = slices.DeleteFunc(e.Map.Zones, func(z otmap.Zone) bool { return z.ID == id })
	e.Map.Each(func(p otmap.Position, t *otmap.Tile) {
		if slices.Contains(t.Zones(), id) {
			nt := tx.Edit(p)
			nt.Ext.Zones = slices.DeleteFunc(nt.Ext.Zones, func(z uint16) bool { return z == id })
			nt.Compact()
		}
	})
	tx.Commit()
}

// ImportOptions control merging another map.
type ImportOptions struct {
	DX int `json:"dx"`
	DY int `json:"dy"`
	DZ int `json:"dz"`
	// Houses: "merge" keeps ids (existing ids win), "renumber" gives
	// imported houses new ids, "skip" drops house tiles' house ids.
	Houses string `json:"houses"`
	// Replace replaces tiles that exist instead of merging items.
	Replace bool `json:"replace"`
	// Towns and waypoints are added when missing.
	Towns bool `json:"towns"`
}

// Import merges another map into this one.
func (e *Editor) Import(src *otmap.Map, o ImportOptions) (int, error) {
	tx := e.Begin("Import map", 0)
	tx.Meta()
	houseIDs := map[uint32]uint32{}
	for _, h := range src.SortedHouses() {
		switch o.Houses {
		case "skip":
			continue
		case "renumber":
			id := uint32(1)
			for e.Map.Houses[id] != nil {
				id++
			}
			houseIDs[h.ID] = id
		default:
			if e.Map.Houses[h.ID] != nil {
				houseIDs[h.ID] = h.ID
				continue
			}
			houseIDs[h.ID] = h.ID
		}
		nh := h.Clone()
		nh.ID = houseIDs[h.ID]
		if ex, ok := nh.Exit.Add(o.DX, o.DY, o.DZ); ok && nh.Exit != (otmap.Position{}) {
			nh.Exit = ex
		}
		if e.Map.Houses[nh.ID] == nil {
			e.Map.Houses[nh.ID] = nh
		}
	}
	if o.Towns {
		for _, t := range src.SortedTowns() {
			if e.Map.Towns[t.ID] == nil {
				c := *t
				if p, ok := c.Temple.Add(o.DX, o.DY, o.DZ); ok {
					c.Temple = p
				}
				e.Map.Towns[t.ID] = &c
			}
		}
		for _, w := range src.Waypoints {
			if e.Map.Waypoint(w.Name) == nil {
				c := *w
				if p, ok := c.Pos.Add(o.DX, o.DY, o.DZ); ok {
					c.Pos = p
				}
				e.Map.Waypoints = append(e.Map.Waypoints, &c)
			}
		}
	}
	var failed error
	src.Each(func(p otmap.Position, t *otmap.Tile) {
		q, ok := p.Add(o.DX, o.DY, o.DZ)
		if !ok {
			failed = errors.New("some tiles were outside the map and were skipped")
			return
		}
		nt := tx.Edit(q)
		in := t.Clone()
		in.SetSelected(false)
		if o.Houses == "skip" {
			in.HouseID = 0
		} else if in.HouseID != 0 {
			in.HouseID = houseIDs[in.HouseID]
		}
		if o.Replace || nt.Empty() {
			state := nt.State
			*nt = *in
			nt.State = state | otmap.StateModified
			return
		}
		if in.Ground.ID != 0 {
			nt.Ground = in.Ground
		}
		for _, it := range in.Items {
			e.addItem(nt, it)
		}
		if in.Ext != nil {
			ext := nt.EnsureExt()
			ext.Creatures = append(ext.Creatures, in.Ext.Creatures...)
			if in.Ext.Spawn != nil {
				ext.Spawn = in.Ext.Spawn
			}
			if in.Ext.SpawnNPC != nil {
				ext.SpawnNPC = in.Ext.SpawnNPC
			}
			ext.Zones = append(ext.Zones, in.Ext.Zones...)
		}
		nt.Flags |= in.Flags
		if in.HouseID != 0 {
			nt.HouseID = in.HouseID
		}
	})
	n := tx.Commit()
	return n, failed
}

// MinimapColor returns the minimap color index of a tile (0 = none): the
// top item with a color, else the ground.
func (e *Editor) MinimapColor(t *otmap.Tile) uint8 {
	for i := len(t.Items) - 1; i >= 0; i-- {
		if typ := e.itemType(t.Items[i].ID); typ != nil && typ.MinimapColor != 0 {
			return uint8(typ.MinimapColor)
		}
	}
	if typ := e.itemType(t.Ground.ID); typ != nil {
		return uint8(typ.MinimapColor)
	}
	return 0
}

// MinimapRGB converts a Tibia minimap color index to RGB.
func MinimapRGB(c uint8) (r, g, b uint8) {
	if c >= 216 {
		return 0, 0, 0
	}
	return uint8(c/36%6) * 51, uint8(c/6%6) * 51, uint8(c%6) * 51
}
