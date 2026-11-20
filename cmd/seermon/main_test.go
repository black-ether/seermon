package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/black-ether/seermon/internal/config"
)

const secret = "hunter2-fixture-secret"

type result struct {
	code        int
	out, errOut string
}

func runApp(t *testing.T, env map[string]string, locs config.Locations, args ...string) result {
	t.Helper()
	var so, se bytes.Buffer
	a := &app{
		stdout: &so,
		stderr: &se,
		getenv: func(k string) string { return env[k] },
		locs:   locs,
	}
	code := a.run(args)
	return result{code, so.String(), se.String()}
}

func emptyLocs(t *testing.T) config.Locations {
	return config.Locations{ExecDir: t.TempDir(), UserConfigDir: t.TempDir()}
}

func TestVersion(t *testing.T) {
	r := runApp(t, nil, emptyLocs(t), "version")
	if r.code != 0 || !strings.HasPrefix(r.out, "seermon dev (") || !strings.Contains(r.out, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("got %+v", r)
	}
	if r := runApp(t, nil, emptyLocs(t), "version", "extra"); r.code != exitUsage {
		t.Fatalf("version with args: got %+v", r)
	}
}

func TestUsageAndUnknown(t *testing.T) {
	if r := runApp(t, nil, emptyLocs(t)); r.code != exitUsage || !strings.Contains(r.errOut, "Usage:") {
		t.Fatalf("bare: %+v", r)
	}
	if r := runApp(t, nil, emptyLocs(t), "bogus"); r.code != exitUsage || !strings.Contains(r.errOut, `unknown command "bogus"`) {
		t.Fatalf("unknown: %+v", r)
	}
	if r := runApp(t, nil, emptyLocs(t), "--help"); r.code != exitOK || !strings.Contains(r.out, "Usage:") {
		t.Fatalf("help: %+v", r)
	}
	if r := runApp(t, nil, emptyLocs(t), "config"); r.code != exitUsage {
		t.Fatalf("config w/o subcommand: %+v", r)
	}
	if r := runApp(t, nil, emptyLocs(t), "config", "check", "--nope"); r.code != exitUsage {
		t.Fatalf("bad flag: %+v", r)
	}
	if r := runApp(t, nil, emptyLocs(t), "config", "check", "stray"); r.code != exitUsage {
		t.Fatalf("stray arg: %+v", r)
	}
}

func TestConfigCheckNoFilesPrintsHintAndPasses(t *testing.T) {
	r := runApp(t, nil, emptyLocs(t), "config", "check")
	if r.code != exitOK {
		t.Fatalf("got %+v", r)
	}
	for _, want := range []string{config.NoConfigHint, "built-in defaults", "(8 targets)", "config ok"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output missing %q:\n%s", want, r.out)
		}
	}
}

func TestInitThenCheckRoundTrip(t *testing.T) {
	locs := emptyLocs(t)
	r := runApp(t, nil, locs, "init")
	if r.code != exitOK || !strings.Contains(r.out, filepath.Join(locs.UserConfigDir, "seermon.yaml")) {
		t.Fatalf("init: %+v", r)
	}

	r = runApp(t, nil, locs, "config", "check")
	if r.code != exitOK || strings.Contains(r.out, config.NoConfigHint) || !strings.Contains(r.out, "config ok") {
		t.Fatalf("check after init: %+v", r)
	}

	// Second init refuses; --force succeeds.
	if r := runApp(t, nil, locs, "init"); r.code != exitFailure || !strings.Contains(r.errOut, "--force") {
		t.Fatalf("re-init: %+v", r)
	}
	if r := runApp(t, nil, locs, "init", "--force"); r.code != exitOK {
		t.Fatalf("init --force: %+v", r)
	}
}

func TestInitDirFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom")
	r := runApp(t, nil, emptyLocs(t), "init", "--dir", dir)
	if r.code != exitOK {
		t.Fatalf("got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "targets.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestInitWithoutAnyDirFails(t *testing.T) {
	if r := runApp(t, nil, config.Locations{}, "init"); r.code != exitFailure || !strings.Contains(r.errOut, "--dir") {
		t.Fatalf("got %+v", r)
	}
}

func TestConfigCheckReportsEveryProblemAndExitsNonZero(t *testing.T) {
	locs := emptyLocs(t)
	os.WriteFile(filepath.Join(locs.UserConfigDir, "seermon.yaml"),
		[]byte("log:\n  retention_days: 0\nthresholds:\n  loss_warn_pct: 9\n  loss_crit_pct: 5\n"), 0o600)
	os.WriteFile(filepath.Join(locs.UserConfigDir, "targets.yaml"),
		[]byte("targets:\n  - { host: a.com, port: 443 }\n  - { host: a.com, port: 443 }\n  - { host: b.com, port: 99999 }\n"), 0o600)

	r := runApp(t, nil, locs, "config", "check")
	if r.code != exitInvalid {
		t.Fatalf("want exit %d, got %+v", exitInvalid, r)
	}
	for _, want := range []string{
		"seermon.yaml: log.retention_days",
		"seermon.yaml: thresholds.loss_warn_pct",
		"targets.yaml: targets[1] (a.com): duplicate target",
		"targets.yaml: targets[2] (b.com): port must be 1-65535",
		"config invalid: 4 problem(s)",
	} {
		if !strings.Contains(r.errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, r.errOut)
		}
	}
	if strings.Contains(r.out, "config ok") {
		t.Fatal("must not claim ok")
	}
}

func TestConfigCheckDeeperFlag(t *testing.T) {
	locs := emptyLocs(t)
	os.WriteFile(filepath.Join(locs.UserConfigDir, "seermon.yaml"), []byte(strings.Join([]string{
		"deeper:",
		"  device: { host: 10.0.0.2 }",
		"  network: { router_lan_ip: 192.168.1.1, device_gateway_ip: 192.168.1.1 }",
		"  failover: { auto: true }",
		"",
	}, "\n")), 0o600)

	if r := runApp(t, nil, locs, "config", "check"); r.code != exitOK {
		t.Fatalf("without --deeper the section is ignored: %+v", r)
	}

	r := runApp(t, nil, locs, "config", "check", "--deeper")
	if r.code != exitInvalid {
		t.Fatalf("with --deeper: %+v", r)
	}
	for _, want := range []string{"must differ", "tiers_enabled is empty", "SEERMON_DEEPER_PW"} {
		if !strings.Contains(r.errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, r.errOut)
		}
	}

	// Fix everything; the password comes from the environment.
	os.WriteFile(filepath.Join(locs.UserConfigDir, "seermon.yaml"), []byte(strings.Join([]string{
		"deeper:",
		"  device: { host: 10.0.0.2 }",
		"  network: { router_lan_ip: 192.168.1.1, device_gateway_ip: 192.168.0.1 }",
		"  failover: { auto: true, tiers_enabled: [t1] }",
		"",
	}, "\n")), 0o600)
	r = runApp(t, map[string]string{"SEERMON_DEEPER_PW": secret}, locs, "config", "check", "--deeper")
	if r.code != exitOK || !strings.Contains(r.out, "deeper rules: applied") {
		t.Fatalf("fixed config: %+v", r)
	}
	if strings.Contains(r.out+r.errOut, secret) {
		t.Fatal("password leaked to output")
	}
}

func TestConfigCheckExplicitPaths(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "my.yaml")
	tgt := filepath.Join(dir, "t.yaml")
	os.WriteFile(cfg, []byte("probe:\n  window: 10\n"), 0o600)
	os.WriteFile(tgt, []byte("targets:\n  - { host: a.com, port: 443 }\n"), 0o600)

	r := runApp(t, nil, emptyLocs(t), "config", "check", "--config", cfg, "--targets-file", tgt)
	if r.code != exitOK || !strings.Contains(r.out, cfg) || !strings.Contains(r.out, "(1 targets)") {
		t.Fatalf("got %+v", r)
	}

	r = runApp(t, nil, emptyLocs(t), "config", "check", "--config", filepath.Join(dir, "missing.yaml"))
	if r.code != exitInvalid || !strings.Contains(r.errOut, "file not found") {
		t.Fatalf("missing explicit: %+v", r)
	}
}
