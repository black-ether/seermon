package config

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/seermon.yaml templates/targets.yaml
var templates embed.FS

// Template returns the embedded default file content ("seermon.yaml" or
// "targets.yaml").
func Template(name string) ([]byte, error) {
	return templates.ReadFile("templates/" + name)
}

// Init writes the default seermon.yaml and targets.yaml into dir (created
// 0700). Existing files are never overwritten unless force is set; with
// force false and any file present, nothing is written. Files are 0600
// because seermon.yaml may later hold a device password.
func Init(dir string, force bool) ([]string, error) {
	names := []string{ConfigFileName, TargetsFileName}

	if !force {
		var existing []string
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
				existing = append(existing, filepath.Join(dir, n))
			}
		}
		if len(existing) > 0 {
			return nil, fmt.Errorf("refusing to overwrite existing file(s): %s (use --force)", strings.Join(existing, ", "))
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	var written []string
	for _, n := range names {
		data, err := Template(n)
		if err != nil {
			return written, err
		}
		path := filepath.Join(dir, n)
		f, err := os.OpenFile(path, flags, 0o600)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return written, fmt.Errorf("refusing to overwrite %s (use --force)", path)
			}
			return written, err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return written, werr
		}
		if cerr != nil {
			return written, cerr
		}
		written = append(written, path)
	}
	return written, nil
}
