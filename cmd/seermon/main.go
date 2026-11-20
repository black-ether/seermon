// Command seermon watches the network path from this machine.
//
// M0 ships the scaffold only: `version`, `init` and `config check`. The live
// feed arrives with v0.1 (M1).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/black-ether/seermon/internal/config"
)

// version is stamped at build time: -ldflags "-X main.version=v0.0.1".
var version = "dev"

// Exit codes for the commands that exist in M0. 2 is the conventional
// usage-error code (and what the flag package uses).
const (
	exitOK      = 0
	exitInvalid = 1 // config check found problems
	exitUsage   = 2
	exitFailure = 3 // could not complete (e.g. init could not write files)
)

func main() {
	a := &app{
		stdout: os.Stdout,
		stderr: os.Stderr,
		getenv: os.Getenv,
		locs:   config.DefaultLocations(),
	}
	os.Exit(a.run(os.Args[1:]))
}

// app carries the process environment so commands are testable.
type app struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	locs           config.Locations
}

const usageText = `seermon — watch the network path from this machine

Usage:
  seermon version
  seermon init [--dir <path>] [--force]
  seermon config check [--config <path>] [--targets-file <path>] [--deeper]

Commands:
  version        print version and build info
  init           write default seermon.yaml and targets.yaml
  config check   validate both config files and exit

The live network feed ships in v0.1.
`

func (a *app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usageText)
		return exitUsage
	}
	switch args[0] {
	case "version":
		return a.cmdVersion(args[1:])
	case "init":
		return a.cmdInit(args[1:])
	case "config":
		return a.cmdConfig(args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, usageText)
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "seermon: unknown command %q\n\n%s", args[0], usageText)
		return exitUsage
	}
}

func (a *app) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	return fs
}

func (a *app) cmdVersion(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(a.stderr, "seermon version: takes no arguments")
		return exitUsage
	}
	fmt.Fprintf(a.stdout, "seermon %s (%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return exitOK
}

func (a *app) cmdInit(args []string) int {
	fs := a.flagSet("seermon init")
	dir := fs.String("dir", "", "directory to write into (default: the user config directory)")
	force := fs.Bool("force", false, "overwrite existing files")
	if code, done := parseFlags(fs, args); done {
		return code
	}

	target := *dir
	if target == "" {
		target = a.locs.UserConfigDir
	}
	if target == "" {
		fmt.Fprintln(a.stderr, "seermon init: cannot determine the user config directory; pass --dir")
		return exitFailure
	}

	written, err := config.Init(target, *force)
	if err != nil {
		fmt.Fprintf(a.stderr, "seermon init: %v\n", err)
		return exitFailure
	}
	for _, p := range written {
		fmt.Fprintf(a.stdout, "wrote %s\n", p)
	}
	fmt.Fprintln(a.stdout, "edit them, then validate with: seermon config check")
	return exitOK
}

func (a *app) cmdConfig(args []string) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(a.stderr, "usage: seermon config check [--config <path>] [--targets-file <path>] [--deeper]\n")
		return exitUsage
	}
	fs := a.flagSet("seermon config check")
	cfgPath := fs.String("config", "", "path to seermon.yaml (default: search)")
	tgtPath := fs.String("targets-file", "", "path to targets.yaml (default: search)")
	deeper := fs.Bool("deeper", false, "also apply the --deeper validation rules")
	if code, done := parseFlags(fs, args[1:]); done {
		return code
	}

	l := config.Load(config.LoadOptions{
		ConfigPath:  *cfgPath,
		TargetsPath: *tgtPath,
		Locations:   a.locs,
		Getenv:      a.getenv,
	})
	problems := l.Check(*deeper)

	for _, w := range l.Warnings {
		fmt.Fprintf(a.stderr, "warning: %s\n", w)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(a.stderr, p)
		}
		fmt.Fprintf(a.stderr, "config invalid: %d problem(s)\n", len(problems))
		return exitInvalid
	}

	if l.NothingFound() {
		fmt.Fprintln(a.stdout, config.NoConfigHint)
	}
	fmt.Fprintf(a.stdout, "%-13s %s\n", config.ConfigFileName+":", describe(l.ConfigPath, "built-in defaults"))
	fmt.Fprintf(a.stdout, "%-13s %s (%d targets)\n", config.TargetsFileName+":", describe(l.TargetsPath, "built-in defaults"), len(l.Targets.Targets))
	if *deeper {
		fmt.Fprintln(a.stdout, "deeper rules: applied")
	}
	fmt.Fprintln(a.stdout, "config ok")
	return exitOK
}

func describe(path, fallback string) string {
	if path == "" {
		return fallback
	}
	return path
}

// parseFlags parses args. done is true when the caller should return code
// immediately (-h, or a flag/usage error).
func parseFlags(fs *flag.FlagSet, args []string) (code int, done bool) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK, true
		}
		return exitUsage, true
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return exitUsage, true
	}
	return 0, false
}
