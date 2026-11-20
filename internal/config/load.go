package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// File names searched for in each location.
const (
	ConfigFileName  = "seermon.yaml"
	TargetsFileName = "targets.yaml"
)

// NoConfigHint is printed once when neither file exists.
const NoConfigHint = "no config found — using defaults; run 'seermon init' to write targets.yaml"

// LoadOptions controls Load.
type LoadOptions struct {
	ConfigPath  string // explicit --config; empty = search
	TargetsPath string // explicit --targets-file; empty = search
	Locations   Locations
	Getenv      func(string) string // defaults to os.Getenv
}

// Problem is one reportable config problem, attributed to a file.
type Problem struct {
	File string // display name: "seermon.yaml" or "targets.yaml"
	Msg  string
}

func (p Problem) String() string { return p.File + ": " + p.Msg }

// Loaded is the result of Load. It is never nil and always usable: files that
// are absent or unreadable leave compiled defaults in place (seermon.yaml) or
// an empty list (an unreadable targets.yaml), and the reason is recorded in
// LoadProblems.
type Loaded struct {
	Config  Config
	Targets Targets

	// Paths of the files actually used; empty means compiled defaults.
	ConfigPath  string
	TargetsPath string

	// LoadProblems are read/parse failures (missing explicit file, bad YAML,
	// unknown key, bad duration). Files with load problems are skipped by
	// Check so one root cause is not reported twice.
	LoadProblems []Problem
	// Warnings are non-fatal (e.g. inline password in a group-readable file).
	Warnings []string

	configFailed  bool
	targetsFailed bool
	getenv        func(string) string
}

// NothingFound reports that neither file was located, so everything is
// compiled defaults.
func (l *Loaded) NothingFound() bool {
	return l.ConfigPath == "" && l.TargetsPath == "" && len(l.LoadProblems) == 0
}

// Load locates and parses both config files independently. Search order for
// each: explicit path → executable directory → user config directory.
func Load(o LoadOptions) *Loaded {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	l := &Loaded{
		Config:  DefaultConfig(),
		Targets: DefaultTargets(),
		getenv:  getenv,
	}

	// seermon.yaml
	path, prob := resolve(o.ConfigPath, ConfigFileName, o.Locations)
	switch {
	case prob != "":
		l.LoadProblems = append(l.LoadProblems, Problem{ConfigFileName, prob})
		l.configFailed = true
	case path != "":
		cfg := DefaultConfig() // decode over defaults: absent keys keep defaults
		if err := decodeFile(path, &cfg); err != nil {
			l.LoadProblems = append(l.LoadProblems, Problem{ConfigFileName, err.Error()})
			l.configFailed = true
		} else {
			l.Config = cfg
			l.ConfigPath = path
			if w := inlinePasswordWarning(path, cfg); w != "" {
				l.Warnings = append(l.Warnings, w)
			}
		}
	}

	// targets.yaml: when the file exists it replaces the defaults entirely, so
	// an absent or empty `targets:` list is an error, not a silent fallback.
	path, prob = resolve(o.TargetsPath, TargetsFileName, o.Locations)
	switch {
	case prob != "":
		l.LoadProblems = append(l.LoadProblems, Problem{TargetsFileName, prob})
		l.targetsFailed = true
	case path != "":
		var ts Targets
		if err := decodeFile(path, &ts); err != nil {
			l.LoadProblems = append(l.LoadProblems, Problem{TargetsFileName, err.Error()})
			l.targetsFailed = true
		} else {
			l.Targets = ts
			l.TargetsPath = path
		}
	}
	return l
}

// Check returns every problem with the loaded configuration: load failures
// plus semantic validation of each file that parsed. deeper enables the
// --deeper-only rules.
func (l *Loaded) Check(deeper bool) []Problem {
	out := append([]Problem(nil), l.LoadProblems...)
	if !l.configFailed {
		for _, m := range l.Config.Validate(ValidateOptions{Deeper: deeper, Getenv: l.getenv}) {
			out = append(out, Problem{ConfigFileName, m})
		}
	}
	if !l.targetsFailed {
		// Validate targets against the global thresholds. If seermon.yaml
		// failed to load, defaults stand in, which keeps targets.yaml
		// independently validatable.
		for _, m := range l.Targets.Validate(l.Config.Thresholds) {
			out = append(out, Problem{TargetsFileName, m})
		}
	}
	return out
}

// resolve finds a config file. An explicit path must exist; otherwise the
// executable directory is tried before the user config directory. It returns
// ("", "") when nothing is found, which is not an error.
func resolve(explicit, name string, locs Locations) (path, problem string) {
	if explicit != "" {
		if st, err := os.Stat(explicit); err != nil {
			return "", fmt.Sprintf("file not found: %s", explicit)
		} else if st.IsDir() {
			return "", fmt.Sprintf("%s is a directory, not a file", explicit)
		}
		return explicit, ""
	}
	for _, dir := range []string{locs.ExecDir, locs.UserConfigDir} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, ""
		}
	}
	return "", ""
}

// decodeFile strictly decodes a YAML file into out: unknown keys are errors
// (they are almost always typos), and an empty document is valid (no
// overrides). Error text is scrubbed of any inline password value.
func decodeFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New(scrubSecrets(err.Error(), nil))
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %s", path, scrubSecrets(cleanYAMLError(err), raw))
	}
	return nil
}

func cleanYAMLError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.ReplaceAll(msg, "unmarshal errors:\n  ", "")
	return strings.ReplaceAll(strings.TrimSpace(msg), "\n  ", "; ")
}

var passwordLine = regexp.MustCompile(`(?m)^\s*password\s*:\s*(.*?)\s*$`)

// scrubSecrets removes any inline `password:` value found in raw from msg.
// YAML decode errors quote offending scalars, so this keeps the "password is
// never echoed" rule true even for parse failures.
func scrubSecrets(msg string, raw []byte) string {
	for _, m := range passwordLine.FindAllSubmatch(raw, -1) {
		if v := yamlScalarValue(string(m[1])); v != "" {
			msg = strings.ReplaceAll(msg, v, "[redacted]")
		}
	}
	return msg
}

// yamlScalarValue extracts the value from the right-hand side of a
// `key: value` line: the contents of a quoted string, or an unquoted value
// up to any trailing ` # comment`.
func yamlScalarValue(rhs string) string {
	rhs = strings.TrimSpace(rhs)
	if rhs == "" {
		return ""
	}
	if q := rhs[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(rhs[1:], q); end >= 0 {
			return rhs[1 : 1+end]
		}
		return rhs[1:]
	}
	if i := strings.Index(rhs, " #"); i >= 0 {
		rhs = rhs[:i]
	}
	return strings.TrimSpace(rhs)
}

// inlinePasswordWarning flags an inline device password in a file that other
// users can read. Unix permission bits only; Windows ACLs are not inspected.
func inlinePasswordWarning(path string, cfg Config) string {
	if cfg.Deeper.Device.Password == "" || runtime.GOOS == "windows" {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("%s holds an inline device password but is readable by other users (mode %04o); run chmod 600 on it, or use password_env instead",
		path, st.Mode().Perm())
}
