// Pushitracker is a desktop item tracker overlay. Each game's icon rows come
// from its folder structure: games/<game>/icons/<row>/<icon>.png.
package main

// The exe's icon is winres/icon.png, built into rsrc_windows_amd64.syso.
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64 --out rsrc

import (
	"os"

	"gioui.org/app"
)

func main() {
	go func() {
		th := newDarkTheme()
		settings := startupSettings()
		for {
			sel := runConfigWindow(th, settings)
			if sel == nil {
				break
			}
			t := NewTracker(sel)
			runTracker(t)
			if !t.ChangeGame {
				break
			}
		}
		os.Exit(0)
	}()
	app.Main()
}
