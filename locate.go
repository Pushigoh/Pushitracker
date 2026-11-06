package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Detected holds the folders found directly beside the exe.
type Detected struct {
	Characters, Games, Settings string
}

// DetectBesideExe looks only in the exe's own folder (not below it) for
// folders named characters, games and settings.
func DetectBesideExe() Detected {
	exe, err := os.Executable()
	if err != nil {
		return Detected{}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return detectIn(filepath.Dir(exe))
}

func detectIn(dir string) Detected {
	var d Detected
	entries, err := os.ReadDir(dir)
	if err != nil {
		return d
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		switch strings.ToLower(e.Name()) {
		case "characters":
			d.Characters = p
		case "games":
			d.Games = p
		case "settings":
			d.Settings = p
		}
	}
	return d
}

// startupSettings picks the settings folder and fills in any folders found
// beside the exe, which take precedence over remembered ones.
func startupSettings() *Settings {
	det := DetectBesideExe()
	dir := det.Settings
	if dir == "" {
		dir = rememberedSettingsDir()
	}
	if dir == "" {
		dir = appDataDir()
	}
	s := LoadSettings(dir)
	if det.Characters != "" {
		s.CharsDir = det.Characters
	}
	if det.Games != "" {
		s.GamesDir = det.Games
	}
	return s
}
