package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nekiro/ots-creator/internal/mapedit"
	"github.com/nekiro/ots-creator/internal/settings"
)

// MapDataConfig tells the map editor where the server data of a client
// lives.
type MapDataConfig struct {
	// ItemsMode is "auto" (items.otb when found), "otb" or "client".
	ItemsMode string `json:"itemsMode"`
	OTBPath   string `json:"otbPath"`
	XMLPath   string `json:"xmlPath"`
	// MaterialsPath is the brush file (JSON). Empty uses the default file
	// of the client version in the settings folder.
	MaterialsPath string `json:"materialsPath"`
	// ServerDir is the server data folder scanned for monsters and NPCs.
	ServerDir string `json:"serverDir"`
}

// RecentMap is a recently opened map.
type RecentMap struct {
	Path string `json:"path"`
	// Client is the client the map was edited with.
	Client string `json:"client"`
}

type mapStoreData struct {
	Profiles map[string]MapDataConfig `json:"profiles"`
	Recent   []RecentMap              `json:"recent"`
	Editor   *mapedit.Settings        `json:"editor"`
	// Keys holds the keyboard shortcuts changed by the user: action id ->
	// key combinations ("Ctrl+S"). Missing actions use their defaults.
	Keys map[string][]string `json:"keys,omitempty"`
}

// MapStore persists map editor settings next to the app settings.
type MapStore struct {
	mu   sync.Mutex
	path string
	d    mapStoreData
}

// maxRecentMaps limits the recent map list.
const maxRecentMaps = 10

// OpenMapStore loads the map editor settings from the settings folder
// (defaults on errors).
func OpenMapStore() *MapStore {
	p, err := settings.DefaultPath()
	if err != nil {
		return NewMapStore("")
	}
	return NewMapStore(filepath.Join(filepath.Dir(p), "mapeditor.json"))
}

// NewMapStore loads map editor settings from path ("" keeps them in
// memory only).
func NewMapStore(path string) *MapStore {
	st := &MapStore{path: path, d: mapStoreData{Profiles: map[string]MapDataConfig{}}}
	if path == "" {
		return st
	}
	data, err := os.ReadFile(st.path)
	if err == nil {
		_ = json.Unmarshal(data, &st.d)
	}
	if st.d.Profiles == nil {
		st.d.Profiles = map[string]MapDataConfig{}
	}
	return st
}

// Dir returns the folder of the settings ("" when unknown).
func (st *MapStore) Dir() string {
	if st.path == "" {
		return ""
	}
	return filepath.Dir(st.path)
}

func (st *MapStore) saveLocked() error {
	if st.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(st.d, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func profileKey(client string) string { return strings.ToLower(filepath.Clean(client)) }

// Profile returns the data config of a client.
func (st *MapStore) Profile(client string) (MapDataConfig, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	c, ok := st.d.Profiles[profileKey(client)]
	return c, ok
}

// SetProfile stores the data config of a client.
func (st *MapStore) SetProfile(client string, c MapDataConfig) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.d.Profiles[profileKey(client)] = c
	return st.saveLocked()
}

// Recent returns the recent maps.
func (st *MapStore) Recent() []RecentMap {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]RecentMap(nil), st.d.Recent...)
}

// AddRecent puts a map first in the recent list.
func (st *MapStore) AddRecent(r RecentMap) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []RecentMap{r}
	for _, x := range st.d.Recent {
		if !strings.EqualFold(filepath.Clean(x.Path), filepath.Clean(r.Path)) {
			out = append(out, x)
		}
	}
	st.d.Recent = out[:min(len(out), maxRecentMaps)]
	return st.saveLocked()
}

// RemoveRecent forgets a recent map ("" clears the list).
func (st *MapStore) RemoveRecent(path string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []RecentMap
	for _, x := range st.d.Recent {
		if path != "" && !strings.EqualFold(filepath.Clean(x.Path), filepath.Clean(path)) {
			out = append(out, x)
		}
	}
	st.d.Recent = out
	return st.saveLocked()
}

// EditorSettings returns the brush behavior settings.
func (st *MapStore) EditorSettings() mapedit.Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.d.Editor == nil {
		return mapedit.DefaultSettings()
	}
	return *st.d.Editor
}

// SetEditorSettings stores the brush behavior settings.
func (st *MapStore) SetEditorSettings(s mapedit.Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.d.Editor = &s
	return st.saveLocked()
}

// Keys returns the changed keyboard shortcuts.
func (st *MapStore) Keys() map[string][]string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return maps.Clone(st.d.Keys)
}

// SetKeys stores the changed keyboard shortcuts (nil restores all defaults).
func (st *MapStore) SetKeys(keys map[string][]string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.d.Keys = maps.Clone(keys)
	return st.saveLocked()
}

// detectServerData looks for items.otb and items.xml in a server data
// folder, a map folder or their parents (data/items, data/world/..).
func detectServerData(start string) MapDataConfig {
	var c MapDataConfig
	if start == "" {
		return c
	}
	dir := start
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		dir = filepath.Dir(dir)
	}
	for i := 0; i < 5 && dir != "" && dir != filepath.Dir(dir); i++ {
		for _, sub := range []string{"items", filepath.Join("data", "items"), ""} {
			base := filepath.Join(dir, sub)
			if c.OTBPath == "" && fileExists(filepath.Join(base, "items.otb")) {
				c.OTBPath = filepath.Join(base, "items.otb")
			}
			if c.XMLPath == "" && fileExists(filepath.Join(base, "items.xml")) {
				c.XMLPath = filepath.Join(base, "items.xml")
			}
		}
		if c.XMLPath != "" {
			c.ServerDir = dir
			if filepath.Base(dir) != "data" && dirExists(filepath.Join(dir, "data")) {
				c.ServerDir = filepath.Join(dir, "data")
			}
			break
		}
		dir = filepath.Dir(dir)
	}
	c.ItemsMode = "auto"
	return c
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

var errNoClient = errors.New("open a client first: the map editor draws the map with its sprites")

// readOptional reads a file, treating "" and a missing file as no data.
func readOptional(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
