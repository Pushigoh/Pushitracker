package main

import (
	"encoding/json"
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
	return detectIn(exeDir())
}

// exeDir is the folder the exe is in, or the working folder if that can't be
// found.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
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

// startupSettings finds the characters and games folders, beside the exe
// unless others were picked with Browse, and starts on the game saved last.
func startupSettings() *Settings { return settingsBeside(exeDir()) }

func settingsBeside(dir string) *Settings {
	det := detectIn(dir)
	s := &Settings{CharsDir: det.Characters, GamesDir: det.Games, home: dir, found: det, legacy: det.Settings}

	// Older versions kept the choices in settings/settings.json.
	var old legacySettings
	if s.legacy != "" {
		if b, err := os.ReadFile(filepath.Join(s.legacy, settingsFile)); err == nil {
			_ = json.Unmarshal(b, &old)
		}
		if s.CharsDir == "" {
			s.CharsDir = old.CharsDir
		}
		if s.GamesDir == "" {
			s.GamesDir = old.GamesDir
		}
		s.Character, s.MicID, s.MicName = old.Character, old.MicID, old.MicName
		s.rewardSecs = old.DisplaySeconds
	}
	s.loadFolders()
	game := s.lastGame()
	if game == "" {
		game = old.Game
	}
	s.SelectGame(game)
	return s
}
