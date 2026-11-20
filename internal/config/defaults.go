package config

import "time"

// DefaultConfig returns the compiled defaults. Nothing site-specific lives
// here: the deeper device/network fields are empty and must come from the
// user's seermon.yaml.
//
// templates/seermon.yaml (written by `seermon init`) must stay in sync with
// this; a test enforces it.
func DefaultConfig() Config {
	return Config{
		Intervals: Intervals{
			Traffic: 1 * time.Second,
			Probe:   2 * time.Second,
			Wifi:    20 * time.Second,
			LAN:     30 * time.Second,
			Geo:     5 * time.Minute,
			CPU:     2 * time.Second,
			Memory:  2 * time.Second,
			GPU:     5 * time.Second,
			Temps:   10 * time.Second,
			Disk:    30 * time.Minute,
		},
		Probe: Probe{
			Timeout: 3 * time.Second,
			Window:  30,
		},
		Thresholds: Thresholds{
			LatencyWarnMs:   300,
			LatencyCritMs:   800,
			LossWarnPct:     1,
			LossCritPct:     5,
			UsageWarnPct:    60,
			UsageCritPct:    85,
			CPUTempWarn:     70,
			CPUTempCrit:     85,
			GPUTempWarn:     75,
			GPUTempCrit:     85,
			TrafficWarnMbps: 1,
			TrafficCritMbps: 10,
		},
		Geo: Geo{
			Enabled:  true,
			Provider: "ipinfo",
			Timeout:  5 * time.Second,
		},
		Log: Log{
			Enabled:       true,
			Dir:           "",
			Interval:      30 * time.Second,
			RetentionDays: 30,
			Rotate:        "daily",
		},
		Feed: Feed{
			Interval: 2 * time.Second,
			Verbose:  false,
			Color:    "auto",
			ASCII:    false,
		},
		Deeper: Deeper{
			Device: DeeperDevice{
				PasswordEnv: "SEERMON_DEEPER_PW",
			},
			DeepProbe: DeepProbe{
				Interval: 30 * time.Second,
				Timeout:  10 * time.Second,
				HTTPSTargets: []HTTPSTarget{
					{Name: "google", URL: "https://www.google.com", ExpectStatus: 200},
					{Name: "reddit", URL: "https://www.reddit.com", ExpectStatus: 200},
				},
				DNSDoHURL:          "https://dns.google/resolve",
				DNSDoHBootstrapIPs: []string{"8.8.8.8", "8.8.4.4"},
				ExitIPEveryNCycles: 10,
			},
			Failover: Failover{
				Auto:                false,
				TiersEnabled:        []string{},
				ConsecutiveFailures: 3,
				VerifyCycles:        2,
				Cooldowns: Cooldowns{
					T1: 5 * time.Minute,
					T2: 10 * time.Minute,
					T3: 15 * time.Minute,
					T4: 20 * time.Minute,
					T5: 10 * time.Minute,
				},
				ModeHold:          10 * time.Minute,
				MaxActionsPerHour: 12,
				MaxRebootsPerDay:  2,
				BootWait:          120 * time.Second,
				HoldDefault:       30 * time.Minute,
				T5: T5{
					PathFailCycles:      6,
					LinkFailCycles:      6,
					ISPFailCycles:       10,
					ReturnAfterRecovery: true,
					ReturnProbeAfter:    10 * time.Minute,
					ReturnTrialWindow:   90 * time.Second,
				},
			},
			Ranking: Ranking{MinSamples: 20},
		},
		Daemon: Daemon{Listen: "127.0.0.1:8788"},
	}
}

// DefaultTargets returns the 8 compiled-in default probe targets. They are
// used only when no targets.yaml is found.
func DefaultTargets() Targets {
	return Targets{Targets: []Target{
		{Host: "bing.com", Port: 443},
		{Host: "google.com", Port: 443},
		{Host: "reddit.com", Port: 443},
		{Host: "github.com", Port: 443},
		{Host: "huggingface.co", Port: 443},
		{Host: "baidu.com", Port: 443, WarnMs: 500, CritMs: 1500},
		{Host: "cloudflare.com", Port: 443},
		{Host: "1.1.1.1", Port: 53},
	}}
}

// KnownTiers are the valid failover tier names.
var KnownTiers = []string{"t1", "t2", "t3", "t4", "t5"}
