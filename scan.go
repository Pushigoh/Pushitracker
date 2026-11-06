package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
type Game struct {
	Name     string
	Rows     []Row
	GifsDir  string
	IdlePath string
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
	iconsFolder = "icons"
	gifsFolder  = "gifs"
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
	return g, nil
}

func (g *Game) IconCount() int {
	n := 0
	for _, r := range g.Rows {
		n += len(r.Icons)
	}
	return n
}

// Character gifs every character folder should have.
const (
	idleGif   = "idle.gif"   // shown at rest
	rewardGif = "reward.gif" // shown on a click when there is no <id>.gif
)

// thisFolder names a characters folder that holds one character's gifs
// directly instead of a folder per character.
const thisFolder = "."

// ListCharacters returns the character folders in the characters folder. If
// it has no subfolders but does have gifs, it is itself the one character.
func ListCharacters(charsDir string) ([]string, error) {
	if charsDir == "" {
		return nil, fmt.Errorf("no characters folder selected")
	}
	chars, err := subdirs(charsDir)
	if err != nil {
		return nil, err
	}
	if len(chars) == 0 && countGifs(charsDir) > 0 {
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

func countGifs(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".gif") {
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

// natLess orders strings naturally: runs of digits compare numerically, so
// "2" < "10".
func natLess(a, b string) bool {
	for a != "" && b != "" {
		ca, ra := natChunk(a)
		cb, rb := natChunk(b)
		if ca != cb {
			da, db := unicode.IsDigit(rune(ca[0])), unicode.IsDigit(rune(cb[0]))
			if da && db {
				na, nb := strings.TrimLeft(ca, "0"), strings.TrimLeft(cb, "0")
				if len(na) != len(nb) {
					return len(na) < len(nb)
				}
				if na != nb {
					return na < nb
				}
			} else {
				return ca < cb
			}
		}
		a, b = ra, rb
	}
	return len(a) < len(b)
}

func natChunk(s string) (chunk, rest string) {
	digit := unicode.IsDigit(rune(s[0]))
	i := 1
	for i < len(s) && unicode.IsDigit(rune(s[i])) == digit {
		i++
	}
	return s[:i], s[i:]
}
