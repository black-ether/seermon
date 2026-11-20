package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const appDir = "seermon"

// Locations are the directories searched for config files, in order:
// explicit flag (handled by the caller) → executable directory → user config
// directory. Either may be empty when it cannot be determined.
type Locations struct {
	ExecDir       string
	UserConfigDir string
}

// DefaultLocations resolves the real locations for this process.
func DefaultLocations() Locations {
	var l Locations
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		l.ExecDir = filepath.Dir(exe)
	}
	if dir, err := UserConfigDir(); err == nil {
		l.UserConfigDir = dir
	}
	return l
}

// UserConfigDir is the per-user config directory for seermon:
// %AppData%\seermon, ~/.config/seermon, or ~/Library/Application Support/seermon.
func UserConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDir), nil
}

// StateDir is where seermon keeps its CSV log and state:
//
//	Windows  %LOCALAPPDATA%\seermon\
//	Linux    ~/.local/state/seermon/   (XDG_STATE_HOME honored)
//	macOS    ~/Library/Application Support/seermon/
func StateDir() (string, error) {
	home, _ := os.UserHomeDir()
	dir := stateDirFor(runtime.GOOS, os.Getenv, home)
	if dir == "" {
		return "", errors.New("cannot determine state directory: no home directory or LOCALAPPDATA")
	}
	return dir, nil
}

// stateDirFor is the pure, testable core of StateDir. It returns "" when the
// needed environment is missing.
func stateDirFor(goos string, getenv func(string) string, home string) string {
	switch goos {
	case "windows":
		if base := getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, appDir)
		}
		if home != "" {
			return filepath.Join(home, "AppData", "Local", appDir)
		}
	case "darwin":
		if home != "" {
			return filepath.Join(home, "Library", "Application Support", appDir)
		}
	default: // linux and other unixes
		if base := getenv("XDG_STATE_HOME"); filepath.IsAbs(base) {
			return filepath.Join(base, appDir)
		}
		if home != "" {
			return filepath.Join(home, ".local", "state", appDir)
		}
	}
	return ""
}

// ResolvedDir returns the CSV log directory: log.dir if set, else the
// platform state directory.
func (l Log) ResolvedDir() (string, error) {
	if l.Dir != "" {
		return l.Dir, nil
	}
	return StateDir()
}
