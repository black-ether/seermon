package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const fixtureSecret = "hunter2-fixture-secret"

func noEnv(string) string { return "" }

func envWith(k, v string) func(string) string {
	return func(name string) string {
		if name == k {
			return v
		}
		return ""
	}
}

func hasProblem(problems []string, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- defaults & templates ---------------------------------------------------

func TestDefaultsAreValid(t *testing.T) {
	if p := DefaultConfig().Validate(ValidateOptions{}); len(p) != 0 {
		t.Fatalf("default config invalid: %v", p)
	}
	if p := DefaultTargets().Validate(DefaultConfig().Thresholds); len(p) != 0 {
		t.Fatalf("default targets invalid: %v", p)
	}
	if n := len(DefaultTargets().Targets); n != 8 {
		t.Fatalf("want 8 default targets, got %d", n)
	}
}

func decodeStrict(t *testing.T, data []byte, out any) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// The embedded templates written by `seermon init` must not drift from the
// compiled defaults.
func TestTemplatesMatchDefaults(t *testing.T) {
	data, err := Template(TargetsFileName)
	if err != nil {
		t.Fatal(err)
	}
	var ts Targets
	decodeStrict(t, data, &ts)
	if !reflect.DeepEqual(ts, DefaultTargets()) {
		t.Errorf("templates/targets.yaml differs from DefaultTargets():\n got %+v\nwant %+v", ts, DefaultTargets())
	}

	data, err = Template(ConfigFileName)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	decodeStrict(t, data, &cfg)
	// The template's site-specific deeper fields are placeholders by design;
	// the compiled defaults leave them empty.
	cfg.Deeper.Device.Host = ""
	cfg.Deeper.Network = DeeperNetwork{}
	cfg.Deeper.DeepProbe.DNSTestDomain = ""
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Errorf("templates/seermon.yaml differs from DefaultConfig():\n got %+v\nwant %+v", cfg, DefaultConfig())
	}
}

func TestTemplateDeeperPlaceholdersAreCaughtOnlyWithDeeper(t *testing.T) {
	data, _ := Template(ConfigFileName)
	var cfg Config
	decodeStrict(t, data, &cfg)

	if p := cfg.Validate(ValidateOptions{}); len(p) != 0 {
		t.Errorf("template must validate cleanly without --deeper, got %v", p)
	}
	p := cfg.Validate(ValidateOptions{Deeper: true, Getenv: envWith("SEERMON_DEEPER_PW", "x")})
	for _, want := range []string{"device.host", "router_lan_ip", "device_gateway_ip", "dns_test_domain", "fallback_wifi[0]"} {
		if !hasProblem(p, want) {
			t.Errorf("--deeper should flag unfilled placeholder %q; got %v", want, p)
		}
	}
}

// ---- §7.3: seermon.yaml rules -----------------------------------------------

func TestConfigValidationCases(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"traffic interval zero", func(c *Config) { c.Intervals.Traffic = 0 }, "intervals.traffic"},
		{"probe interval negative", func(c *Config) { c.Intervals.Probe = -time.Second }, "intervals.probe"},
		{"disk interval zero", func(c *Config) { c.Intervals.Disk = 0 }, "intervals.disk"},
		{"probe timeout zero", func(c *Config) { c.Probe.Timeout = 0 }, "probe.timeout"},
		{"probe window zero", func(c *Config) { c.Probe.Window = 0 }, "probe.window"},
		{"geo timeout zero", func(c *Config) { c.Geo.Timeout = 0 }, "geo.timeout"},
		{"geo provider unknown", func(c *Config) { c.Geo.Provider = "nope" }, "geo.provider"},
		{"log interval below probe", func(c *Config) { c.Log.Interval = time.Second }, "log.interval (1s) must be >= intervals.probe (2s)"},
		{"retention zero", func(c *Config) { c.Log.RetentionDays = 0 }, "log.retention_days"},
		{"rotate unknown", func(c *Config) { c.Log.Rotate = "weekly" }, "log.rotate"},
		{"feed interval zero", func(c *Config) { c.Feed.Interval = 0 }, "feed.interval"},
		{"feed color unknown", func(c *Config) { c.Feed.Color = "rainbow" }, "feed.color"},
		{"loss crit over 100", func(c *Config) { c.Thresholds.LossCritPct = 150 }, "loss_crit_pct must be <= 100"},
		{"usage crit over 100", func(c *Config) { c.Thresholds.UsageCritPct = 150 }, "usage_crit_pct must be <= 100"},
		{"listen all interfaces", func(c *Config) { c.Daemon.Listen = "0.0.0.0:8788" }, "loopback"},
		{"listen LAN address", func(c *Config) { c.Daemon.Listen = "192.168.1.5:8788" }, "loopback"},
		{"listen malformed", func(c *Config) { c.Daemon.Listen = "8788" }, "not host:port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mutate(&c)
			p := c.Validate(ValidateOptions{})
			if !hasProblem(p, tc.want) {
				t.Fatalf("want a problem containing %q, got %v", tc.want, p)
			}
		})
	}
}

func TestLoopbackListenAccepted(t *testing.T) {
	for _, l := range []string{"127.0.0.1:8788", "[::1]:8788", "localhost:8788", ""} {
		c := DefaultConfig()
		c.Daemon.Listen = l
		if p := c.Validate(ValidateOptions{}); len(p) != 0 {
			t.Errorf("listen %q should be valid, got %v", l, p)
		}
	}
}

// Every warn/crit pair must be strictly ordered: equal and inverted both fail.
func TestEveryThresholdPairMustBeOrdered(t *testing.T) {
	pairs := []struct {
		name       string
		warn, crit func(*Config) *float64
	}{
		{"latency", func(c *Config) *float64 { return &c.Thresholds.LatencyWarnMs }, func(c *Config) *float64 { return &c.Thresholds.LatencyCritMs }},
		{"loss", func(c *Config) *float64 { return &c.Thresholds.LossWarnPct }, func(c *Config) *float64 { return &c.Thresholds.LossCritPct }},
		{"usage", func(c *Config) *float64 { return &c.Thresholds.UsageWarnPct }, func(c *Config) *float64 { return &c.Thresholds.UsageCritPct }},
		{"cpu_temp", func(c *Config) *float64 { return &c.Thresholds.CPUTempWarn }, func(c *Config) *float64 { return &c.Thresholds.CPUTempCrit }},
		{"gpu_temp", func(c *Config) *float64 { return &c.Thresholds.GPUTempWarn }, func(c *Config) *float64 { return &c.Thresholds.GPUTempCrit }},
		{"traffic", func(c *Config) *float64 { return &c.Thresholds.TrafficWarnMbps }, func(c *Config) *float64 { return &c.Thresholds.TrafficCritMbps }},
	}
	for _, pr := range pairs {
		t.Run(pr.name+"/equal", func(t *testing.T) {
			c := DefaultConfig()
			*pr.warn(&c) = *pr.crit(&c)
			if p := c.Validate(ValidateOptions{}); !hasProblem(p, "must be less than") {
				t.Fatalf("equal warn/crit must fail, got %v", p)
			}
		})
		t.Run(pr.name+"/inverted", func(t *testing.T) {
			c := DefaultConfig()
			*pr.warn(&c) = *pr.crit(&c) + 1
			if p := c.Validate(ValidateOptions{}); !hasProblem(p, "must be less than") {
				t.Fatalf("inverted warn/crit must fail, got %v", p)
			}
		})
	}
}

// ---- §7.3: targets.yaml rules -----------------------------------------------

func TestTargetsValidationCases(t *testing.T) {
	g := DefaultConfig().Thresholds
	cases := []struct {
		name string
		ts   []Target
		want string
	}{
		{"empty list", nil, "at least one target"},
		{"empty host", []Target{{Host: "", Port: 443}}, "host must not be empty"},
		{"blank host", []Target{{Host: "   ", Port: 443}}, "host must not be empty"},
		{"port zero", []Target{{Host: "a.com", Port: 0}}, "port must be 1-65535"},
		{"port negative", []Target{{Host: "a.com", Port: -1}}, "port must be 1-65535"},
		{"port too large", []Target{{Host: "a.com", Port: 65536}}, "port must be 1-65535"},
		{"duplicate", []Target{{Host: "a.com", Port: 443}, {Host: "a.com", Port: 443}}, "duplicate target a.com:443"},
		{"duplicate case-insensitive", []Target{{Host: "A.com", Port: 443}, {Host: "a.COM", Port: 443}}, "duplicate target"},
		{"url instead of host", []Target{{Host: "https://a.com", Port: 443}}, "bare hostname"},
		{"host with port", []Target{{Host: "a.com:443", Port: 443}}, "bare hostname"},
		{"warn >= crit override", []Target{{Host: "a.com", Port: 443, WarnMs: 900, CritMs: 800}}, "must be less than"},
		{"lone warn above global crit", []Target{{Host: "a.com", Port: 443, WarnMs: 900}}, "must be less than"},
		{"lone crit below global warn", []Target{{Host: "a.com", Port: 443, CritMs: 100}}, "must be less than"},
		{"negative override", []Target{{Host: "a.com", Port: 443, WarnMs: -1}}, "must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Targets{Targets: tc.ts}.Validate(g)
			if !hasProblem(p, tc.want) {
				t.Fatalf("want a problem containing %q, got %v", tc.want, p)
			}
		})
	}
}

func TestTargetsAcceptedForms(t *testing.T) {
	ts := Targets{Targets: []Target{
		{Host: "a.com", Port: 443},
		{Host: "a.com", Port: 80}, // same host, different port is fine
		{Host: "1.1.1.1", Port: 53},
		{Host: "2606:4700:4700::1111", Port: 53}, // IPv6 literal
		{Host: "b.com", Port: 443, WarnMs: 500, CritMs: 1500},
		{Host: "c.com", Port: 443, WarnMs: 0, CritMs: 0}, // 0 = use global
	}}
	if p := ts.Validate(DefaultConfig().Thresholds); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

// ---- §7.3: --deeper rules ---------------------------------------------------

func validDeeperConfig() Config {
	c := DefaultConfig()
	c.Deeper.Device.Host = "10.0.0.2"
	c.Deeper.Network.RouterLANIP = "192.168.1.1"
	c.Deeper.Network.DeviceGatewayIP = "192.168.0.1"
	return c
}

var deeperOpts = ValidateOptions{Deeper: true, Getenv: envWith("SEERMON_DEEPER_PW", fixtureSecret)}

func TestValidDeeperConfigPasses(t *testing.T) {
	if p := validDeeperConfig().Validate(deeperOpts); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

func TestDeeperValidationCases(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		opts   ValidateOptions
		want   string
	}{
		{"router and device gateway equal", func(c *Config) { c.Deeper.Network.DeviceGatewayIP = c.Deeper.Network.RouterLANIP }, deeperOpts, "must differ"},
		{"equal across spellings", func(c *Config) {
			c.Deeper.Network.RouterLANIP = "::ffff:192.168.1.1"
			c.Deeper.Network.DeviceGatewayIP = "192.168.1.1"
		}, deeperOpts, "must differ"},
		{"missing router ip", func(c *Config) { c.Deeper.Network.RouterLANIP = "" }, deeperOpts, "router_lan_ip is required"},
		{"bad device gateway ip", func(c *Config) { c.Deeper.Network.DeviceGatewayIP = "not-an-ip" }, deeperOpts, "not a valid IP"},
		{"bad upstream ip", func(c *Config) { c.Deeper.Network.DeviceUpstreamIP = "999.1.1.1" }, deeperOpts, "device_upstream_ip"},
		{"missing device host", func(c *Config) { c.Deeper.Device.Host = "" }, deeperOpts, "device.host is required"},
		{"password env unset", func(c *Config) {}, ValidateOptions{Deeper: true, Getenv: noEnv}, "SEERMON_DEEPER_PW"},
		{"password env named differently", func(c *Config) { c.Deeper.Device.PasswordEnv = "MY_PW" }, ValidateOptions{Deeper: true, Getenv: noEnv}, "MY_PW"},
		{"no password source at all", func(c *Config) { c.Deeper.Device.PasswordEnv = "" }, ValidateOptions{Deeper: true, Getenv: noEnv}, "password_env"},
		{"unknown tier", func(c *Config) { c.Deeper.Failover.TiersEnabled = []string{"t1", "t9"} }, deeperOpts, `unknown tier "t9"`},
		{"auto without tiers", func(c *Config) { c.Deeper.Failover.Auto = true }, deeperOpts, "tiers_enabled is empty"},
		{"zero consecutive failures", func(c *Config) { c.Deeper.Failover.ConsecutiveFailures = 0 }, deeperOpts, "consecutive_failures"},
		{"zero verify cycles", func(c *Config) { c.Deeper.Failover.VerifyCycles = 0 }, deeperOpts, "verify_cycles"},
		{"zero actions per hour", func(c *Config) { c.Deeper.Failover.MaxActionsPerHour = 0 }, deeperOpts, "max_actions_per_hour"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validDeeperConfig()
			tc.mutate(&c)
			p := c.Validate(tc.opts)
			if !hasProblem(p, tc.want) {
				t.Fatalf("want a problem containing %q, got %v", tc.want, p)
			}
		})
	}
}

func TestDeeperAutoWithTiersIsValid(t *testing.T) {
	c := validDeeperConfig()
	c.Deeper.Failover.Auto = true
	c.Deeper.Failover.TiersEnabled = []string{"t1"}
	if p := c.Validate(deeperOpts); len(p) != 0 {
		t.Fatalf("auto + [t1] should be valid, got %v", p)
	}
}

func TestDeeperRulesIgnoredWithoutDeeperFlag(t *testing.T) {
	c := DefaultConfig() // empty device/network: would fail every deeper rule
	c.Deeper.Failover.Auto = true
	c.Deeper.Failover.TiersEnabled = []string{"bogus"}
	if p := c.Validate(ValidateOptions{}); len(p) != 0 {
		t.Fatalf("deeper section must be ignored without --deeper, got %v", p)
	}
}

func TestInlinePasswordSatisfiesRequirement(t *testing.T) {
	c := validDeeperConfig()
	c.Deeper.Device.Password = fixtureSecret
	if p := c.Validate(ValidateOptions{Deeper: true, Getenv: noEnv}); len(p) != 0 {
		t.Fatalf("inline password should satisfy the requirement, got %v", p)
	}
}

func TestEnvPasswordPreferredOverInline(t *testing.T) {
	c := validDeeperConfig()
	c.Deeper.Device.Password = "inline"
	got, ok := c.DevicePassword(envWith("SEERMON_DEEPER_PW", "from-env"))
	if !ok || got != "from-env" {
		t.Fatalf("env should win, got %q ok=%v", got, ok)
	}
}

// The password value must never appear in any validation message.
func TestPasswordNeverEchoedInProblems(t *testing.T) {
	c := validDeeperConfig()
	c.Deeper.Device.Password = fixtureSecret
	c.Deeper.Network.DeviceGatewayIP = c.Deeper.Network.RouterLANIP // force problems
	c.Deeper.Failover.Auto = true
	c.Thresholds.LatencyWarnMs = c.Thresholds.LatencyCritMs
	for _, getenv := range []func(string) string{noEnv, envWith("SEERMON_DEEPER_PW", fixtureSecret)} {
		for _, m := range c.Validate(ValidateOptions{Deeper: true, Getenv: getenv}) {
			if strings.Contains(m, fixtureSecret) {
				t.Fatalf("problem leaks password: %q", m)
			}
		}
	}
}

// ---- loading ------------------------------------------------------------------

func TestLoadNothingFoundUsesDefaults(t *testing.T) {
	l := Load(LoadOptions{Locations: Locations{ExecDir: t.TempDir(), UserConfigDir: t.TempDir()}, Getenv: noEnv})
	if !l.NothingFound() {
		t.Fatal("expected NothingFound")
	}
	if !reflect.DeepEqual(l.Config, DefaultConfig()) || !reflect.DeepEqual(l.Targets, DefaultTargets()) {
		t.Fatal("expected compiled defaults")
	}
	if p := l.Check(false); len(p) != 0 {
		t.Fatalf("defaults must check clean, got %v", p)
	}
}

func TestSearchOrderExecDirBeforeUserDir(t *testing.T) {
	execDir, userDir := t.TempDir(), t.TempDir()
	write(t, execDir, ConfigFileName, "probe:\n  window: 11\n")
	write(t, userDir, ConfigFileName, "probe:\n  window: 22\n")
	l := Load(LoadOptions{Locations: Locations{ExecDir: execDir, UserConfigDir: userDir}, Getenv: noEnv})
	if l.Config.Probe.Window != 11 {
		t.Fatalf("exec dir should win, got window %d", l.Config.Probe.Window)
	}

	os.Remove(filepath.Join(execDir, ConfigFileName))
	l = Load(LoadOptions{Locations: Locations{ExecDir: execDir, UserConfigDir: userDir}, Getenv: noEnv})
	if l.Config.Probe.Window != 22 {
		t.Fatalf("user dir should be the fallback, got window %d", l.Config.Probe.Window)
	}
}

func TestExplicitPathBeatsSearch(t *testing.T) {
	execDir := t.TempDir()
	write(t, execDir, ConfigFileName, "probe:\n  window: 11\n")
	explicit := write(t, t.TempDir(), "custom.yaml", "probe:\n  window: 33\n")
	l := Load(LoadOptions{ConfigPath: explicit, Locations: Locations{ExecDir: execDir}, Getenv: noEnv})
	if l.Config.Probe.Window != 33 || l.ConfigPath != explicit {
		t.Fatalf("explicit path should win, got window %d path %q", l.Config.Probe.Window, l.ConfigPath)
	}
}

func TestExplicitMissingFileIsAProblem(t *testing.T) {
	l := Load(LoadOptions{ConfigPath: filepath.Join(t.TempDir(), "nope.yaml"), TargetsPath: filepath.Join(t.TempDir(), "nope2.yaml"), Getenv: noEnv})
	p := l.Check(false)
	if len(p) != 2 || !strings.Contains(p[0].Msg, "file not found") || !strings.Contains(p[1].Msg, "file not found") {
		t.Fatalf("want two file-not-found problems, got %v", p)
	}
}

func TestPartialConfigKeepsDefaultsForOmittedKeys(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, "probe:\n  window: 11\n")
	l := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv})
	want := DefaultConfig()
	want.Probe.Window = 11
	if !reflect.DeepEqual(l.Config, want) {
		t.Fatalf("omitted keys must keep defaults:\n got %+v\nwant %+v", l.Config, want)
	}
}

func TestEmptyConfigFileIsValidAndEmptyTargetsFileIsNot(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, "")
	write(t, dir, TargetsFileName, "# nothing here\n")
	l := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv})
	p := l.Check(false)
	if len(p) != 1 || p[0].File != TargetsFileName || !strings.Contains(p[0].Msg, "at least one target") {
		t.Fatalf("want exactly the empty-targets problem, got %v", p)
	}
}

func TestBadYAMLProblems(t *testing.T) {
	cases := []struct {
		name, file, body, want string
	}{
		{"unknown key (typo)", ConfigFileName, "probe:\n  windw: 5\n", "windw"},
		{"unparseable duration", ConfigFileName, "intervals:\n  traffic: soon\n", "line 2: cannot unmarshal !!str `soon` into time.Duration"},
		{"bare-number duration", ConfigFileName, "probe:\n  timeout: 3\n", "line 2: cannot unmarshal !!int `3` into time.Duration"},
		{"wrong type", ConfigFileName, "probe:\n  window: many\n", "line 2: cannot unmarshal !!str `many` into int"},
		{"broken yaml", ConfigFileName, "probe: [unclosed\n", "line"},
		{"unknown target key", TargetsFileName, "targets:\n  - { host: a.com, port: 443, warn_msec: 5 }\n", "warn_msec"},
		{"target port not a number", TargetsFileName, "targets:\n  - { host: a.com, port: https }\n", "into int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.file, tc.body)
			p := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv}).Check(false)
			if len(p) == 0 {
				t.Fatal("expected a problem")
			}
			if p[0].File != tc.file || !strings.Contains(p[0].Msg, tc.want) {
				t.Fatalf("want %s problem containing %q, got %v", tc.file, tc.want, p)
			}
		})
	}
}

// Each file is validated independently: a broken seermon.yaml must not hide
// problems in targets.yaml (or the reverse), and a parse failure is not
// reported a second time as a semantic problem.
func TestFilesAreCheckedIndependently(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, "probe:\n  window: many\n")
	write(t, dir, TargetsFileName, "targets:\n  - { host: a.com, port: 0 }\n")
	p := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv}).Check(false)
	var cfg, tgt int
	for _, x := range p {
		switch x.File {
		case ConfigFileName:
			cfg++
		case TargetsFileName:
			tgt++
		}
	}
	if cfg != 1 || tgt != 1 {
		t.Fatalf("want one problem per file, got %v", p)
	}
}

func TestSemanticProblemsFromBothFilesReportedTogether(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, "log:\n  retention_days: 0\n")
	write(t, dir, TargetsFileName, "targets:\n  - { host: a.com, port: 443 }\n  - { host: a.com, port: 443 }\n")
	p := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv}).Check(false)
	if len(p) != 2 {
		t.Fatalf("want 2 problems, got %v", p)
	}
}

func TestTargetOverrideChecksAgainstConfiguredGlobals(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, "thresholds:\n  latency_warn_ms: 100\n  latency_crit_ms: 200\n")
	write(t, dir, TargetsFileName, "targets:\n  - { host: a.com, port: 443, warn_ms: 250 }\n")
	p := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv}).Check(false)
	if len(p) != 1 || p[0].File != TargetsFileName {
		t.Fatalf("lone warn 250 vs configured crit 200 should fail, got %v", p)
	}
}

func TestCheckWithDeeperUsesEnvironment(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, strings.Join([]string{
		"deeper:",
		"  device: { host: 10.0.0.2 }",
		"  network: { router_lan_ip: 192.168.1.1, device_gateway_ip: 192.168.0.1 }",
		"",
	}, "\n"))
	locs := Locations{UserConfigDir: dir}
	if p := Load(LoadOptions{Locations: locs, Getenv: noEnv}).Check(true); !hasProblem(problemMsgs(p), "SEERMON_DEEPER_PW") {
		t.Fatalf("missing env password should be flagged, got %v", p)
	}
	if p := Load(LoadOptions{Locations: locs, Getenv: envWith("SEERMON_DEEPER_PW", fixtureSecret)}).Check(true); len(p) != 0 {
		t.Fatalf("with the env var set it should pass, got %v", p)
	}
	if p := Load(LoadOptions{Locations: locs, Getenv: noEnv}).Check(false); len(p) != 0 {
		t.Fatalf("without --deeper the deeper section is ignored, got %v", p)
	}
}

func problemMsgs(ps []Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// YAML decode errors quote the offending scalar. If that scalar happens to be
// the password, it must be scrubbed.
func TestPasswordScrubbedFromParseErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ConfigFileName, "probe:\n  window: "+fixtureSecret+"\ndeeper:\n  device:\n    password: "+fixtureSecret+"\n")
	p := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv}).Check(true)
	if len(p) == 0 {
		t.Fatal("expected a parse problem")
	}
	for _, x := range problemMsgs(p) {
		if strings.Contains(x, fixtureSecret) {
			t.Fatalf("parse error leaks password: %q", x)
		}
	}
}

func TestScrubSecrets(t *testing.T) {
	raw := []byte("device:\n  password: \"s3cret!\"   # comment\n  other: x\n")
	got := scrubSecrets("cannot unmarshal `s3cret!` into int", raw)
	if strings.Contains(got, "s3cret!") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("got %q", got)
	}
	if scrubSecrets("untouched", nil) != "untouched" {
		t.Fatal("nil raw must be a no-op")
	}
}

func TestInlinePasswordPermissionWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits only")
	}
	dir := t.TempDir()
	path := write(t, dir, ConfigFileName, "deeper:\n  device:\n    password: "+fixtureSecret+"\n")

	l := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv})
	if len(l.Warnings) != 0 {
		t.Fatalf("0600 file should not warn, got %v", l.Warnings)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	l = Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv})
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "chmod 600") {
		t.Fatalf("0644 file with inline password should warn, got %v", l.Warnings)
	}
	if strings.Contains(l.Warnings[0], fixtureSecret) {
		t.Fatal("warning leaks the password")
	}
}

// ---- init -----------------------------------------------------------------------

func TestInitWritesLoadableFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "seermon")
	written, err := Init(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Fatalf("want 2 files, got %v", written)
	}
	if runtime.GOOS != "windows" {
		for _, p := range written {
			if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %v, want 0600", p, st.Mode().Perm())
			}
		}
	}
	l := Load(LoadOptions{Locations: Locations{UserConfigDir: dir}, Getenv: noEnv})
	if p := l.Check(false); len(p) != 0 {
		t.Fatalf("freshly written files must check clean: %v", p)
	}
	if l.ConfigPath == "" || l.TargetsPath == "" {
		t.Fatal("init output should be discovered by the search path")
	}
}

func TestInitRefusesToOverwriteAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	existing := write(t, dir, TargetsFileName, "targets: []\n")

	if _, err := Init(dir, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want overwrite refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); err == nil {
		t.Fatal("refused init must not write the other file")
	}
	if b, _ := os.ReadFile(existing); string(b) != "targets: []\n" {
		t.Fatal("existing file was modified")
	}

	if _, err := Init(dir, true); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
	if b, _ := os.ReadFile(existing); string(b) == "targets: []\n" {
		t.Fatal("--force did not overwrite")
	}
}

// ---- paths ----------------------------------------------------------------------

func TestStateDirPerPlatform(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		home string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\x\AppData\Local`}, `C:\Users\x`, filepath.Join(`C:\Users\x\AppData\Local`, "seermon")},
		{"windows", nil, "/h", filepath.Join("/h", "AppData", "Local", "seermon")},
		{"linux", nil, "/home/x", filepath.Join("/home/x", ".local", "state", "seermon")},
		{"linux", map[string]string{"XDG_STATE_HOME": "/xdg/state"}, "/home/x", filepath.Join("/xdg/state", "seermon")},
		{"linux", map[string]string{"XDG_STATE_HOME": "relative/ignored"}, "/home/x", filepath.Join("/home/x", ".local", "state", "seermon")},
		{"darwin", nil, "/Users/x", filepath.Join("/Users/x", "Library", "Application Support", "seermon")},
		{"linux", nil, "", ""},
	}
	for _, tc := range cases {
		got := stateDirFor(tc.goos, func(k string) string { return tc.env[k] }, tc.home)
		if got != tc.want {
			t.Errorf("%s env=%v home=%q: got %q want %q", tc.goos, tc.env, tc.home, got, tc.want)
		}
	}
}

func TestLogResolvedDir(t *testing.T) {
	if d, err := (Log{Dir: "/custom"}).ResolvedDir(); err != nil || d != "/custom" {
		t.Fatalf("got %q %v", d, err)
	}
}
