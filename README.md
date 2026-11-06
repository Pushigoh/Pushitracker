# Pushitracker

## Build

Needs Go 1.27+.

```
build.bat
```

To change the exe's icon, replace `winres/icon.png` and run `go generate`.

## Folders

Put these next to `pushitracker.exe`:

```
pushitracker.exe
characters/
  <character>/
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
settings/
```

