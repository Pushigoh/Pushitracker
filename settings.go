package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	settingsFile    = "settings.json"
	locationFile    = "location.json"
	MinIconSize     = 16
	MaxIconSize     = 128
	DefaultIconSize = 32
	MinCharSize     = 100
	MaxCharSize     = 1000
	DefaultCharSize = 350
	MinItemSize     = 32
	MaxItemSize     = 800
	DefaultItemSize = 150
)

// Settings are the setup choices, kept in <settings folder>/settings.json.
// Each game's tracker settings are in their own file next to it (see
// GameSettings). The settings folder is either a "settings" folder next to
// the exe, the one last picked in setup, or %AppData%\pushitracker.
type Settings struct {
	CharsDir       string `json:"charsDir"`
	GamesDir       string `json:"gamesDir"`
	Character      string `json:"character"`
	Game           string `json:"game"`
	DisplaySeconds int    `json:"displaySeconds"`

	dir string // the settings folder this was loaded from and saves to
}

// GameSettings are one game's tracker settings, kept in
// <settings folder>/<game>.json and written when Save is pressed in the
// settings panel.
type GameSettings struct {
	IconSize    int     `json:"iconSize"`
	CharSize    int     `json:"characterSize"` // height in px
	ItemSize    int     `json:"itemSize"`
	AlwaysOnTop bool    `json:"alwaysOnTop"`
	TrackerPos  *[2]int `json:"trackerPos,omitempty"`
	CharPos     *[2]int `json:"characterPos,omitempty"`
	ItemPos     *[2]int `json:"itemPos,omitempty"`
	PanelPos    *[2]int `json:"settingsPanelPos,omitempty"`
	// Icons dragged out of their place in their row: how far, in icon
	// sizes, by "<row>/<icon>".
	IconPos map[string][2]float64 `json:"iconPositions,omitempty"`

	path string
}

// appDataDir is the fallback settings folder.
func appDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "pushitracker")
}

// LoadSettings reads dir/settings.json, filling in defaults for anything
// missing. A missing or unreadable file just gives the defaults.
func LoadSettings(dir string) *Settings {
	s := &Settings{DisplaySeconds: 8}
	if b, err := os.ReadFile(filepath.Join(dir, settingsFile)); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.DisplaySeconds <= 0 {
		s.DisplaySeconds = 8
	}
	s.dir = dir
	return s
}

func (s *Settings) Dir() string { return s.dir }

func (s *Settings) Save() error { return writeJSON(filepath.Join(s.dir, settingsFile), s) }

func (s *Settings) gamePath(game string) string { return filepath.Join(s.dir, game+".json") }

// LoadGameSettings reads one game's file, with defaults if there isn't one.
func (s *Settings) LoadGameSettings(game string) *GameSettings {
	g := &GameSettings{IconSize: DefaultIconSize, CharSize: DefaultCharSize, ItemSize: DefaultItemSize, path: s.gamePath(game)}
	if b, err := os.ReadFile(g.path); err == nil {
		_ = json.Unmarshal(b, g)
	}
	g.IconSize = clampIconSize(g.IconSize)
	g.CharSize = clampCharSize(g.CharSize)
	g.ItemSize = clampSize(g.ItemSize, MinItemSize, MaxItemSize, DefaultItemSize)
	return g
}

// SavedGames lists the games that have a settings file.
func (s *Settings) SavedGames() []string {
	entries, _ := os.ReadDir(s.dir)
	var games []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.EqualFold(filepath.Ext(name), ".json") || name == settingsFile || name == locationFile {
			continue
		}
		games = append(games, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	sort.Slice(games, func(i, j int) bool { return natLess(games[i], games[j]) })
	return games
}

func (g *GameSettings) Save() error { return writeJSON(g.path, g) }

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func clampIconSize(n int) int { return clampSize(n, MinIconSize, MaxIconSize, DefaultIconSize) }
func clampCharSize(n int) int { return clampSize(n, MinCharSize, MaxCharSize, DefaultCharSize) }

func clampSize(n, lo, hi, def int) int {
	if n <= 0 {
		return def
	}
	return min(max(n, lo), hi)
}

// rememberSettingsDir records the chosen settings folder so it is used again
// when no "settings" folder sits next to the exe.
func rememberSettingsDir(dir string) {
	b, _ := json.Marshal(map[string]string{"settingsDir": dir})
	_ = os.MkdirAll(appDataDir(), 0o755)
	_ = os.WriteFile(filepath.Join(appDataDir(), locationFile), b, 0o644)
}

func rememberedSettingsDir() string {
	var loc struct {
		SettingsDir string `json:"settingsDir"`
	}
	if b, err := os.ReadFile(filepath.Join(appDataDir(), locationFile)); err == nil {
		_ = json.Unmarshal(b, &loc)
	}
	if loc.SettingsDir != "" && isDir(loc.SettingsDir) {
		return loc.SettingsDir
	}
	return ""
}
