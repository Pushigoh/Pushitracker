@echo off
rem Builds a standalone pushitracker.exe (no console window, no runtime needed).
cd /d "%~dp0"
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags "-H windowsgui -s -w" -o pushitracker.exe .
if errorlevel 1 exit /b 1
echo Built %~dp0pushitracker.exe
