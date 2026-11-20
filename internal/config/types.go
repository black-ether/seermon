// Package config loads and validates seermon's two user-owned config files:
// seermon.yaml (everything except targets) and targets.yaml (probe targets).
//
// Both files are optional; compiled defaults apply when neither exists.
// Nothing site-specific is ever compiled in.
package config

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// Config mirrors seermon.yaml.
type Config struct {
	Intervals  Intervals  `yaml:"intervals"`
	Probe      Probe      `yaml:"probe"`
	Thresholds Thresholds `yaml:"thresholds"`
	Geo        Geo        `yaml:"geo"`
	Log        Log        `yaml:"log"`
	Feed       Feed       `yaml:"feed"`
	Deeper     Deeper     `yaml:"deeper"`
	Daemon     Daemon     `yaml:"daemon"`
}

// Intervals are collector cadences. cpu..disk only run with --full.
type Intervals struct {
	Traffic time.Duration `yaml:"traffic"`
	Probe   time.Duration `yaml:"probe"`
	Wifi    time.Duration `yaml:"wifi"`
	LAN     time.Duration `yaml:"lan"`
	Geo     time.Duration `yaml:"geo"`
	CPU     time.Duration `yaml:"cpu"`
	Memory  time.Duration `yaml:"memory"`
	GPU     time.Duration `yaml:"gpu"`
	Temps   time.Duration `yaml:"temps"`
	Disk    time.Duration `yaml:"disk"`
}

// Probe controls target probing.
type Probe struct {
	Timeout time.Duration `yaml:"timeout"` // a timeout counts as a loss sample
	Window  int           `yaml:"window"`  // rolling samples per target
}

// Thresholds follow one rule everywhere: v >= crit is red, v >= warn is amber.
type Thresholds struct {
	LatencyWarnMs   float64 `yaml:"latency_warn_ms"`
	LatencyCritMs   float64 `yaml:"latency_crit_ms"`
	LossWarnPct     float64 `yaml:"loss_warn_pct"`
	LossCritPct     float64 `yaml:"loss_crit_pct"`
	UsageWarnPct    float64 `yaml:"usage_warn_pct"`
	UsageCritPct    float64 `yaml:"usage_crit_pct"`
	CPUTempWarn     float64 `yaml:"cpu_temp_warn"`
	CPUTempCrit     float64 `yaml:"cpu_temp_crit"`
	GPUTempWarn     float64 `yaml:"gpu_temp_warn"`
	GPUTempCrit     float64 `yaml:"gpu_temp_crit"`
	TrafficWarnMbps float64 `yaml:"traffic_warn_mbps"`
	TrafficCritMbps float64 `yaml:"traffic_crit_mbps"`
}

// Geo controls the public IP / geolocation lookup.
type Geo struct {
	Enabled  bool          `yaml:"enabled"`
	Provider string        `yaml:"provider"`
	Timeout  time.Duration `yaml:"timeout"`
}

// Log controls the CSV log.
type Log struct {
	Enabled       bool          `yaml:"enabled"`
	Dir           string        `yaml:"dir"` // empty = platform state dir
	Interval      time.Duration `yaml:"interval"`
	RetentionDays int           `yaml:"retention_days"`
	Rotate        string        `yaml:"rotate"`
}

// Feed controls the append-only feed renderer.
type Feed struct {
	Interval time.Duration `yaml:"interval"`
	Verbose  bool          `yaml:"verbose"`
	Color    string        `yaml:"color"` // auto|always|never
	ASCII    bool          `yaml:"ascii"`
}

// Deeper is v0.4+ and ignored entirely without --deeper.
type Deeper struct {
	Device    DeeperDevice  `yaml:"device"`
	Network   DeeperNetwork `yaml:"network"`
	DeepProbe DeepProbe     `yaml:"deep_probe"`
	Failover  Failover      `yaml:"failover"`
	Ranking   Ranking       `yaml:"ranking"`
}

// DeeperDevice locates and authenticates to the Deeper Connect device.
// The password is a secret: it must never appear in logs, CSV, JSON, IPC
// responses or error messages.
type DeeperDevice struct {
	Host        string `yaml:"host"`
	PasswordEnv string `yaml:"password_env"` // preferred
	Password    string `yaml:"password"`     // inline fallback (perm-warned)
}

// DeeperNetwork describes the PC/device/router topology.
type DeeperNetwork struct {
	DeeperWifi       string   `yaml:"deeper_wifi"`
	UpstreamWifi     string   `yaml:"upstream_wifi"`
	FallbackWifi     []string `yaml:"fallback_wifi"`
	RouterLANIP      string   `yaml:"router_lan_ip"`
	DeviceGatewayIP  string   `yaml:"device_gateway_ip"`
	DeviceUpstreamIP string   `yaml:"device_upstream_ip"`
}

// HTTPSTarget is one deep-cycle reachability target.
type HTTPSTarget struct {
	Name         string `yaml:"name"`
	URL          string `yaml:"url"`
	ExpectStatus int    `yaml:"expect_status"`
}

// DeepProbe configures the 30 s deep cycle.
type DeepProbe struct {
	Interval           time.Duration `yaml:"interval"`
	Timeout            time.Duration `yaml:"timeout"`
	HTTPSTargets       []HTTPSTarget `yaml:"https_targets"`
	DNSTestDomain      string        `yaml:"dns_test_domain"`
	DNSDoHURL          string        `yaml:"dns_doh_url"`
	DNSDoHBootstrapIPs []string      `yaml:"dns_doh_bootstrap_ips"`
	ExitIPEveryNCycles int           `yaml:"exit_ip_every_n_cycles"`
}

// Cooldowns are per-tier cooldowns.
type Cooldowns struct {
	T1 time.Duration `yaml:"t1"`
	T2 time.Duration `yaml:"t2"`
	T3 time.Duration `yaml:"t3"`
	T4 time.Duration `yaml:"t4"`
	T5 time.Duration `yaml:"t5"`
}

// T5 configures the Windows Wi-Fi fallback tier.
type T5 struct {
	PathFailCycles      int           `yaml:"path_fail_cycles"`
	LinkFailCycles      int           `yaml:"link_fail_cycles"`
	ISPFailCycles       int           `yaml:"isp_fail_cycles"`
	ReturnAfterRecovery bool          `yaml:"return_after_recovery"`
	ReturnProbeAfter    time.Duration `yaml:"return_probe_after"`
	ReturnTrialWindow   time.Duration `yaml:"return_trial_window"`
}

// Failover configures guarded local failover. Automation needs two keys:
// Auto == true here AND running as `seermon daemon`.
type Failover struct {
	Auto                bool          `yaml:"auto"`
	TiersEnabled        []string      `yaml:"tiers_enabled"`
	ConsecutiveFailures int           `yaml:"consecutive_failures"`
	VerifyCycles        int           `yaml:"verify_cycles"`
	Cooldowns           Cooldowns     `yaml:"cooldowns"`
	ModeHold            time.Duration `yaml:"mode_hold"`
	MaxActionsPerHour   int           `yaml:"max_actions_per_hour"`
	MaxRebootsPerDay    int           `yaml:"max_reboots_per_day"`
	BootWait            time.Duration `yaml:"boot_wait"`
	HoldDefault         time.Duration `yaml:"hold_default"`
	T5                  T5            `yaml:"t5"`
}

// Ranking configures node ranking.
type Ranking struct {
	MinSamples int `yaml:"min_samples"`
}

// Daemon is v0.6+.
type Daemon struct {
	Listen string `yaml:"listen"` // loopback only
}

// Target is one probe target from targets.yaml.
type Target struct {
	Host   string  `yaml:"host"`
	Port   int     `yaml:"port"`
	WarnMs float64 `yaml:"warn_ms,omitempty"` // 0/absent = global threshold
	CritMs float64 `yaml:"crit_ms,omitempty"`
}

// Key is the canonical host:port identity used for duplicate detection.
func (t Target) Key() string {
	return net.JoinHostPort(strings.ToLower(strings.TrimSpace(t.Host)), strconv.Itoa(t.Port))
}

// EffectiveLatency returns the latency thresholds for this target: the
// per-target override where set, otherwise the global value.
func (t Target) EffectiveLatency(g Thresholds) (warn, crit float64) {
	warn, crit = g.LatencyWarnMs, g.LatencyCritMs
	if t.WarnMs > 0 {
		warn = t.WarnMs
	}
	if t.CritMs > 0 {
		crit = t.CritMs
	}
	return warn, crit
}

// Targets mirrors targets.yaml.
type Targets struct {
	Targets []Target `yaml:"targets"`
}
