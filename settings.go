package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	settingsFile    = "settings.json"
	settingsFolder  = "settings"
	MinIconSize     = 16
	MaxIconSize     = 128
	DefaultIconSize = 32
	MinCharSize     = 100
	MaxCharSize     = 1000
	DefaultCharSize = 350
	MinItemSize     = 32
	MaxItemSize     = 800
	DefaultItemSize = 150
	MaxIconSpacing  = 64
	// DefaultIconSpacing is the space between icons in a row.
	DefaultIconSpacing = 10
	MaxRowSpacing      = 64
	MinRewardSeconds   = 1
	MaxRewardSeconds   = 60
	MaxItemDelayMs     = 10000
	// Mic levels, in dB below full scale: the character starts talking over
	// the open threshold and stops under the close threshold.
	MinTalkThreshold = -60
	MaxTalkThreshold = 0
	DefaultTalkOpen  = -40
	DefaultTalkClose = -50
	// How long the character keeps talking after the level drops.
	MaxTalkHoldMs     = 2000
	DefaultTalkHoldMs = 250
	MinOverlaySize    = 10 // percent of the overlay's own size
	MaxOverlaySize    = 400
)

// Settings are the setup choices. The character and microphone are saved
// with each game (see GameSettings), and setup starts on the game saved last.
// The games and characters folders are the ones beside the exe, unless others
// have been picked with Browse: only then is a settings.json kept beside the
// exe, holding just those folders (see folderFile).
type Settings struct {
	CharsDir  string
	GamesDir  string
	Character string
	Game      string
	// The microphone the character talks with: Windows' ID for it (empty for
	// none) and its name when it was chosen.
	MicID   string
	MicName string

	home  string   // the exe's folder, where folderFile goes ("" for none)
	found Detected // the folders found beside the exe
	// The settings folder older versions kept beside the exe, with every
	// game's file in it: a game's file there is read until the game saves
	// its own.
	legacy       string
	rewardSecs   int  // the reward time for games without one saved
	savedChoices bool // the game's own choices were loaded
}

// GameSettings are one game's tracker settings, kept in
// games/<game>/settings/settings.json. The setup choices are written when
// Start is pressed, and the rest when Save is pressed in the settings panel.
type GameSettings struct {
	IconSize    int `json:"iconSize"`
	IconSpacing int `json:"iconSpacing"`   // space between icons in a row
	RowSpacing  int `json:"rowSpacing"`    // space between rows
	CharSize    int `json:"characterSize"` // height in px
	ItemSize    int `json:"itemSize"`
	// How long the character and item show a clicked icon's gifs, in seconds.
	RewardSeconds int `json:"rewardSeconds"`
	// How long after a click the item display changes; the character
	// changes at once.
	ItemDelayMs int `json:"itemDelayMs"`
	// When the mic gets louder than TalkOpenDb (dBFS), the character shows
	// its talk image, until it has been quieter than TalkCloseDb for
	// TalkHoldMs.
	TalkOpenDb  int     `json:"talkOpenDb"`
	TalkCloseDb int     `json:"talkCloseDb"`
	TalkHoldMs  int     `json:"talkHoldMs"`
	AlwaysOnTop bool    `json:"alwaysOnTop"`
	TrackerPos  *[2]int `json:"trackerPos,omitempty"`
	CharPos     *[2]int `json:"characterPos,omitempty"`
	ItemPos     *[2]int `json:"itemPos,omitempty"`
	PanelPos    *[2]int `json:"settingsPanelPos,omitempty"`
	// Icons dragged out of their place in their row: how far, in icon
	// sizes, by "<row>/<icon>".
	IconPos map[string][2]float64 `json:"iconPositions,omitempty"`
	// Where each overlay is relative to the tracker, by file name.
	OverlayPos map[string][2]int `json:"overlayPositions,omitempty"`
	// Overlays shown at other than their own size, in percent, by file name.
	OverlaySize map[string]int `json:"overlaySizes,omitempty"`

	// The choices made in setup for this game: the character ("" for none)
	// and the mic.
	Character string `json:"character"`
	MicID     string `json:"microphone,omitempty"`
	MicName   string `json:"microphoneName,omitempty"`

	path string
}

// legacySettings is the setup file of older versions.
type legacySettings struct {
	CharsDir       string `json:"charsDir"`
	GamesDir       string `json:"gamesDir"`
	Character      string `json:"character"`
	Game           string `json:"game"`
	DisplaySeconds int    `json:"displaySeconds"`
	MicID          string `json:"microphone"`
	MicName        string `json:"microphoneName"`
}

// defaultRewardSeconds is how long a reward shows until a game sets its own.
const defaultRewardSeconds = 8

// SelectGame picks a game, and the character and mic saved with it if it has
// any; otherwise the current ones stay.
func (s *Settings) SelectGame(name string) {
	s.Game, s.savedChoices = name, false
	p := s.gamePath(name)
	if p == "" || !fileExists(p) {
		return
	}
	g := s.LoadGameSettings(name)
	s.Character, s.MicID, s.MicName = g.Character, g.MicID, g.MicName
	s.savedChoices = true
}

// ChoseNoCharacter reports whether the game was saved with no character.
func (s *Settings) ChoseNoCharacter() bool { return s.savedChoices && s.Character == "" }

// lastGame is the game whose settings were saved most recently, or "".
func (s *Settings) lastGame() string {
	games, _ := subdirs(s.GamesDir)
	var last string
	var at time.Time
	for _, g := range games {
		if fi, err := os.Stat(s.gamePath(g)); err == nil && fi.ModTime().After(at) {
			last, at = g, fi.ModTime()
		}
	}
	return last
}

// Save writes the setup choices into the chosen game's settings, and the
// folders into folderFile if they aren't the ones beside the exe.
func (s *Settings) Save() error {
	if err := s.saveFolders(); err != nil {
		return err
	}
	if s.Game == "" {
		return nil
	}
	g := s.LoadGameSettings(s.Game)
	s.stamp(g)
	return g.Save()
}

// folderFile is settings.json beside the exe. It exists only while a
// characters or games folder other than the one beside the exe is in use.
type folderFile struct {
	CharsDir string `json:"charactersFolder,omitempty"`
	GamesDir string `json:"gamesFolder,omitempty"`
}

// loadFolders uses the folders saved in folderFile, if any.
func (s *Settings) loadFolders() {
	if s.home == "" {
		return
	}
	var f folderFile
	if b, err := os.ReadFile(filepath.Join(s.home, settingsFile)); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if f.CharsDir != "" {
		s.CharsDir = f.CharsDir
	}
	if f.GamesDir != "" {
		s.GamesDir = f.GamesDir
	}
}

// saveFolders writes the folders that differ from the ones beside the exe to
// folderFile, or removes it when none do.
func (s *Settings) saveFolders() error {
	if s.home == "" {
		return nil
	}
	var f folderFile
	if !samePath(s.CharsDir, s.found.Characters) {
		f.CharsDir = s.CharsDir
	}
	if !samePath(s.GamesDir, s.found.Games) {
		f.GamesDir = s.GamesDir
	}
	p := filepath.Join(s.home, settingsFile)
	if f == (folderFile{}) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeJSON(p, f)
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

// stamp copies the setup choices into a game's settings.
func (s *Settings) stamp(g *GameSettings) {
	g.Character, g.MicID, g.MicName = s.Character, s.MicID, s.MicName
}

// gamePath is where a game's settings are kept, or "" with no games folder.
func (s *Settings) gamePath(game string) string {
	if s.GamesDir == "" || game == "" {
		return ""
	}
	return filepath.Join(s.GamesDir, game, settingsFolder, settingsFile)
}

// LoadGameSettings reads one game's file, with defaults if there isn't one.
func (s *Settings) LoadGameSettings(game string) *GameSettings {
	g := &GameSettings{
		IconSize: DefaultIconSize, IconSpacing: DefaultIconSpacing,
		CharSize: DefaultCharSize, ItemSize: DefaultItemSize, path: s.gamePath(game),
		RewardSeconds: s.rewardSecs,
		TalkOpenDb:    DefaultTalkOpen, TalkCloseDb: DefaultTalkClose, TalkHoldMs: DefaultTalkHoldMs,
	}
	if b, err := os.ReadFile(g.path); err == nil {
		_ = json.Unmarshal(b, g)
	} else if s.legacy != "" {
		if b, err := os.ReadFile(filepath.Join(s.legacy, game+".json")); err == nil {
			_ = json.Unmarshal(b, g)
		}
	}
	if g.RewardSeconds <= 0 {
		g.RewardSeconds = defaultRewardSeconds
	}
	g.IconSize = clampIconSize(g.IconSize)
	g.CharSize = clampCharSize(g.CharSize)
	g.ItemSize = clampSize(g.ItemSize, MinItemSize, MaxItemSize, DefaultItemSize)
	g.IconSpacing = min(max(g.IconSpacing, 0), MaxIconSpacing)
	g.RowSpacing = min(max(g.RowSpacing, 0), MaxRowSpacing)
	g.RewardSeconds = min(max(g.RewardSeconds, MinRewardSeconds), MaxRewardSeconds)
	g.ItemDelayMs = min(max(g.ItemDelayMs, 0), MaxItemDelayMs)
	g.TalkOpenDb = min(max(g.TalkOpenDb, MinTalkThreshold), MaxTalkThreshold)
	g.TalkCloseDb = min(max(g.TalkCloseDb, MinTalkThreshold), MaxTalkThreshold)
	g.TalkHoldMs = min(max(g.TalkHoldMs, 0), MaxTalkHoldMs)
	for k, v := range g.OverlaySize {
		g.OverlaySize[k] = clampSize(v, MinOverlaySize, MaxOverlaySize, 100)
	}
	return g
}

func (g *GameSettings) Save() error {
	if g.path == "" {
		return nil
	}
	return writeJSON(g.path, g)
}

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
