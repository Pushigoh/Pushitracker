# Pushitracker

<p align="center">
  <img src="docs/screenshot.png" alt="Pushitracker with icon rows, an item and a character" width="380">
</p>

## Download

Get the latest version from the [Releases page](https://github.com/Pushigoh/Pushitracker/releases/latest).

## Build

Needs Go 1.27+.

```
build.bat
```
## Folders

Put these next to `pushitracker.exe`:

```
pushitracker.exe
characters/
  <character>/      optional; each image can be a .gif, .png or .jpg
    idle.gif        shown at rest
    reward.gif      shown when an icon is clicked
    <n>.gif         shown instead of reward.gif for icon <n> (optional)
games/
  <game>/
    icons/
      0.png         item display when nothing is clicked (optional)
      <row>/<n>.png               one icon; each folder is one row
      <row>/<n>-1.png, <n>-2.png  an icon with several stages
    gifs/
      <n>.gif       shown when icon <n> is clicked
    overlays/
      <name>.png    drawn behind everything else (optional); a .gif plays
settings/
```

## Controls

- Click an icon to collect it, or move it to its next stage. Right-click it to clear it.
- Drag the grip to move everything.
- Shift+drag the character, the item or an overlay to move it.
- Ctrl+drag an icon to move just that icon, or Alt+drag it to move its whole row.
- Ctrl+Z undoes the last move.
- The gear opens the settings. Save keeps sizes and positions for this game.

