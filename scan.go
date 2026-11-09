package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Game is one folder under the games folder (e.g. "oot", "mm", "tp"). Each
// game has its own icons and gifs; nothing is shared between games.
//
//	games/<game>/icons/<row>/<n>.png   icons, one row per folder
//	games/<game>/icons/0.png           idle item-display image (optional)
//	games/<game>/gifs/<n>.gif          item animation on click
//	games/<game>/overlays/<name>.png   images behind everything (optional)
type Game struct {
	Name     string
	Rows     []Row
	GifsDir  string
	IdlePath string
	Overlays []Overlay
}

// Overlay is an image drawn behind the icons, character and item.
type Overlay struct {
	Name string // file name without extension, which its position is saved under
	Path string
}

// Row is one subfolder of a game; every icon in it is drawn on one line.
type Row struct {
	Name  string
	Icons []IconDef
}

// IconDef is one clickable icon. A plain icon ("22.png") has a single stage;
// a multi-stage icon ("28-1.png", "28-2.png", ...) has one stage per file.
type IconDef struct {
	Key    string
	Stages []Stage
}

// Stage is one image of an icon. ID is the file name without extension and is
// also the name looked up in gifs/ and in the character folder.
type Stage struct {
	ID   string
	Path string
}

// Folders inside each game.
const (
	iconsFolder    = "icons"
	gifsFolder     = "gifs"
	overlaysFolder = "overlays"
)

var stageName = regexp.MustCompile(`^(.+)-(\d+)$`)

var iconExts = map[string]bool{".png": true, ".gif": true, ".jpg": true, ".jpeg": true}

// ListGames returns the folders in the games folder that have an icons
// folder; anything else there is ignored.
func ListGames(gamesDir string) ([]string, error) {
	if gamesDir == "" {
		return nil, fmt.Errorf("no games folder selected")
	}
	dirs, err := subdirs(gamesDir)
	if err != nil {
		return nil, err
	}
	var games []string
	for _, d := range dirs {
		if isDir(filepath.Join(gamesDir, d, iconsFolder)) {
			games = append(games, d)
		}
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("no game folders with an %s folder in %s", iconsFolder, gamesDir)
	}
	return games, nil
}

func LoadGame(gamesDir, name string) (*Game, error) {
	dir := filepath.Join(gamesDir, name)
	icons := filepath.Join(dir, iconsFolder)
	g := &Game{Name: name, GifsDir: filepath.Join(dir, gifsFolder)}
	if p := filepath.Join(icons, "0.png"); fileExists(p) {
		g.IdlePath = p
	}

	rowNames, err := subdirs(icons)
	if err != nil {
		return nil, err
	}
	for _, r := range rowNames {
		row, err := scanRow(filepath.Join(icons, r))
		if err != nil {
			return nil, err
		}
		g.Rows = append(g.Rows, Row{Name: r, Icons: row})
	}
	if len(g.Rows) == 0 {
		return nil, fmt.Errorf("%s has no row folders", icons)
	}
	g.Overlays = scanOverlays(filepath.Join(dir, overlaysFolder))
	return g, nil
}

// scanOverlays lists the images in a game's overlays folder, which is
// optional.
func scanOverlays(dir string) []Overlay {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && iconExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return natLess(names[i], names[j]) })
	var overlays []Overlay
	for _, n := range names {
		overlays = append(overlays, Overlay{Name: strings.TrimSuffix(n, filepath.Ext(n)), Path: filepath.Join(dir, n)})
	}
	return overlays
}

func (g *Game) IconCount() int {
	n := 0
	for _, r := range g.Rows {
		n += len(r.Icons)
	}
	return n
}

// Images every character folder should have, as .gif, .png or .jpg.
const (
	idleName   = "idle"   // shown at rest
	rewardName = "reward" // shown on a click when there is no <id> image
	// talk is shown instead of idle while the mic is loud enough, and
	// <name>_talk instead of a reward image <name>, if there is one.
	talkName   = "talk"
	talkSuffix = "_talk"
)

// charExts are the kinds of character image, in the order they are looked
// for: a gif wins over a png or jpg of the same name.
var charExts = []string{".gif", ".png", ".jpg", ".jpeg"}

// characterImage returns the path of the character image called name in dir,
// or "" if there is none (or no character).
func characterImage(dir, name string) string {
	if dir == "" {
		return ""
	}
	for _, ext := range charExts {
		if p := filepath.Join(dir, name+ext); fileExists(p) {
			return p
		}
	}
	return ""
}

// gameCharDir returns the character's folder of images for one game (named
// like the game's folder, in any case), or "" if it has none.
func gameCharDir(charDir, game string) string {
	if charDir == "" {
		return ""
	}
	entries, _ := os.ReadDir(charDir)
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), game) {
			return filepath.Join(charDir, e.Name())
		}
	}
	return ""
}

// thisFolder names a characters folder that holds one character's images
// directly instead of a folder per character.
const thisFolder = "."

// ListCharacters returns the character folders in the characters folder. If
// it has an idle image itself (its subfolders then being per-game images),
// or images and no subfolders, it is itself the one character.
func ListCharacters(charsDir string) ([]string, error) {
	if charsDir == "" {
		return nil, fmt.Errorf("no characters folder selected")
	}
	chars, err := subdirs(charsDir)
	if err != nil {
		return nil, err
	}
	if characterImage(charsDir, idleName) != "" || len(chars) == 0 && countCharImages(charsDir) > 0 {
		chars = []string{thisFolder}
	}
	if len(chars) == 0 {
		return nil, fmt.Errorf("no character folders in %s", charsDir)
	}
	return chars, nil
}

func characterLabel(charsDir, name string) string {
	if name == thisFolder {
		return filepath.Base(charsDir)
	}
	return name
}

// countCharImages counts the character images in dir.
func countCharImages(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if !e.IsDir() && slices.Contains(charExts, strings.ToLower(filepath.Ext(e.Name()))) {
			n++
		}
	}
	return n
}

func scanRow(dir string) ([]IconDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && iconExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return natLess(names[i], names[j]) })

	var icons []IconDef
	byKey := map[string]int{}
	for _, name := range names {
		id := strings.TrimSuffix(name, filepath.Ext(name))
		st := Stage{ID: id, Path: filepath.Join(dir, name)}
		m := stageName.FindStringSubmatch(id)
		if m == nil {
			icons = append(icons, IconDef{Key: id, Stages: []Stage{st}})
			continue
		}
		// Multi-stage icon: group all "<base>-<n>" files under the first one's slot.
		if i, ok := byKey[m[1]]; ok {
			icons[i].Stages = append(icons[i].Stages, st)
		} else {
			byKey[m[1]] = len(icons)
			icons = append(icons, IconDef{Key: m[1], Stages: []Stage{st}})
		}
	}
	for i := range icons {
		stages := icons[i].Stages
		sort.SliceStable(stages, func(a, b int) bool { return stageNum(stages[a].ID) < stageNum(stages[b].ID) })
	}
	return icons, nil
}

func stageNum(id string) int {
	m := stageName.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

func subdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return natLess(names[i], names[j]) })
	return names, nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// natLess orders strings naturally and ignoring case: runs of digits
// compare numerically, so "2" < "10", and "bow" < "Hookshot" < "zora". Names
// that differ only in case still get a fixed order.
func natLess(a, b string) bool {
	if c := natCompare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c < 0
	}
	return natCompare(a, b) < 0
}

// natCompare is natLess's comparison, case and all: -1, 0 or 1.
func natCompare(a, b string) int {
	for a != "" && b != "" {
		ca, ra := natChunk(a)
		cb, rb := natChunk(b)
		if ca != cb {
			da, db := unicode.IsDigit(rune(ca[0])), unicode.IsDigit(rune(cb[0]))
			if da && db {
				na, nb := strings.TrimLeft(ca, "0"), strings.TrimLeft(cb, "0")
				if len(na) != len(nb) {
					return cmp.Compare(len(na), len(nb))
				}
				if na != nb {
					return strings.Compare(na, nb)
				}
			} else {
				return strings.Compare(ca, cb)
			}
		}
		a, b = ra, rb
	}
	return cmp.Compare(len(a), len(b))
}

func natChunk(s string) (chunk, rest string) {
	digit := unicode.IsDigit(rune(s[0]))
	i := 1
	for i < len(s) && unicode.IsDigit(rune(s[i])) == digit {
		i++
	}
	return s[:i], s[i:]
}
