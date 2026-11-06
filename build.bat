@echo off
rem Builds a standalone pushitracker.exe (no console window, no runtime needed).
rem The version comes from VERSION if set, else from a git tag on this commit.
cd /d "%~dp0"
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
if not defined VERSION for /f %%v in ('git describe --tags --exact-match 2^>nul') do set VERSION=%%v
go build -trimpath -ldflags "-H windowsgui -s -w -X main.version=%VERSION%" -o pushitracker.exe .
if errorlevel 1 exit /b 1
echo Built %~dp0pushitracker.exe %VERSION%
