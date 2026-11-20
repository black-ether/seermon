package config

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// ValidateOptions selects which rules apply.
type ValidateOptions struct {
	// Deeper enables the --deeper-only rules. Without it the deeper section
	// is ignored entirely.
	Deeper bool
	// Getenv resolves password_env. Defaults to "nothing set" when nil.
	Getenv func(string) string
}

// problems accumulates validation messages. Messages may name fields and
// numeric values but must never include the device password.
type problems []string

func (p *problems) addf(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func (p *problems) positive(field string, d time.Duration) {
	if d <= 0 {
		p.addf("%s must be a positive duration (got %s)", field, d)
	}
}

func (p *problems) atLeast(field string, v, min int) {
	if v < min {
		p.addf("%s must be >= %d (got %d)", field, min, v)
	}
}

func (p *problems) oneOf(field, v string, allowed ...string) {
	for _, a := range allowed {
		if v == a {
			return
		}
	}
	p.addf("%s must be one of %s (got %q)", field, strings.Join(allowed, "|"), v)
}

// pair checks the warn < crit ordering and non-negative values.
func (p *problems) pair(warnField, critField string, warn, crit float64) {
	if warn < 0 {
		p.addf("%s must be >= 0 (got %v)", warnField, warn)
	}
	if crit < 0 {
		p.addf("%s must be >= 0 (got %v)", critField, crit)
	}
	if warn >= crit {
		p.addf("%s (%v) must be less than %s (%v)", warnField, warn, critField, crit)
	}
}

// Validate checks seermon.yaml semantics and returns every problem found.
// Duration syntax is checked earlier, at parse time.
func (c Config) Validate(o ValidateOptions) []string {
	var p problems

	for _, iv := range []struct {
		name string
		d    time.Duration
	}{
		{"intervals.traffic", c.Intervals.Traffic},
		{"intervals.probe", c.Intervals.Probe},
		{"intervals.wifi", c.Intervals.Wifi},
		{"intervals.lan", c.Intervals.LAN},
		{"intervals.geo", c.Intervals.Geo},
		{"intervals.cpu", c.Intervals.CPU},
		{"intervals.memory", c.Intervals.Memory},
		{"intervals.gpu", c.Intervals.GPU},
		{"intervals.temps", c.Intervals.Temps},
		{"intervals.disk", c.Intervals.Disk},
	} {
		p.positive(iv.name, iv.d)
	}

	p.positive("probe.timeout", c.Probe.Timeout)
	p.atLeast("probe.window", c.Probe.Window, 1)

	t := c.Thresholds
	p.pair("thresholds.latency_warn_ms", "thresholds.latency_crit_ms", t.LatencyWarnMs, t.LatencyCritMs)
	p.pair("thresholds.loss_warn_pct", "thresholds.loss_crit_pct", t.LossWarnPct, t.LossCritPct)
	p.pair("thresholds.usage_warn_pct", "thresholds.usage_crit_pct", t.UsageWarnPct, t.UsageCritPct)
	p.pair("thresholds.cpu_temp_warn", "thresholds.cpu_temp_crit", t.CPUTempWarn, t.CPUTempCrit)
	p.pair("thresholds.gpu_temp_warn", "thresholds.gpu_temp_crit", t.GPUTempWarn, t.GPUTempCrit)
	p.pair("thresholds.traffic_warn_mbps", "thresholds.traffic_crit_mbps", t.TrafficWarnMbps, t.TrafficCritMbps)
	if t.LossCritPct > 100 {
		p.addf("thresholds.loss_crit_pct must be <= 100 (got %v)", t.LossCritPct)
	}
	if t.UsageCritPct > 100 {
		p.addf("thresholds.usage_crit_pct must be <= 100 (got %v)", t.UsageCritPct)
	}

	p.positive("geo.timeout", c.Geo.Timeout)
	if c.Geo.Enabled {
		p.oneOf("geo.provider", c.Geo.Provider, "ipinfo")
	}

	p.positive("log.interval", c.Log.Interval)
	if c.Log.Interval > 0 && c.Intervals.Probe > 0 && c.Log.Interval < c.Intervals.Probe {
		p.addf("log.interval (%s) must be >= intervals.probe (%s)", c.Log.Interval, c.Intervals.Probe)
	}
	p.atLeast("log.retention_days", c.Log.RetentionDays, 1)
	p.oneOf("log.rotate", c.Log.Rotate, "daily")

	p.positive("feed.interval", c.Feed.Interval)
	p.oneOf("feed.color", c.Feed.Color, "auto", "always", "never")

	if c.Daemon.Listen != "" {
		validateLoopbackListen(&p, c.Daemon.Listen)
	}

	if o.Deeper {
		c.validateDeeper(&p, o)
	}
	return p
}

// validateLoopbackListen enforces "loopback only; never 0.0.0.0 in v1".
func validateLoopbackListen(p *problems, listen string) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		p.addf("daemon.listen %q is not host:port", listen)
		return
	}
	if n, err := net.LookupPort("tcp", port); err != nil || n < 1 {
		p.addf("daemon.listen port %q is invalid", port)
	}
	if host == "localhost" {
		return
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		p.addf("daemon.listen host %q must be a loopback address (127.0.0.1, ::1 or localhost); non-loopback listeners are not supported in v1", host)
	}
}

func isPlaceholder(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 2 && strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">")
}

// DevicePassword resolves the Deeper device password: the environment
// variable named by password_env first, then the inline password. The caller
// must never log, print or serialize the returned value.
func (c Config) DevicePassword(getenv func(string) string) (string, bool) {
	d := c.Deeper.Device
	if d.PasswordEnv != "" && getenv != nil {
		if v := getenv(d.PasswordEnv); v != "" {
			return v, true
		}
	}
	if d.Password != "" {
		return d.Password, true
	}
	return "", false
}

func (c Config) validateDeeper(p *problems, o ValidateOptions) {
	d := c.Deeper

	// Device.
	switch {
	case strings.TrimSpace(d.Device.Host) == "":
		p.addf("deeper.device.host is required with --deeper")
	case isPlaceholder(d.Device.Host):
		p.addf("deeper.device.host is still the placeholder %q; set the device's management address", d.Device.Host)
	}
	if _, ok := c.DevicePassword(o.Getenv); !ok {
		// Names only: never the value.
		if d.Device.PasswordEnv != "" {
			p.addf("deeper.device: no password available; set the %s environment variable (or the inline deeper.device.password key)", d.Device.PasswordEnv)
		} else {
			p.addf("deeper.device: no password available; set deeper.device.password_env (preferred) or deeper.device.password")
		}
	}

	// Network.
	n := d.Network
	router, routerOK := parseRequiredIP(p, "deeper.network.router_lan_ip", n.RouterLANIP)
	gw, gwOK := parseRequiredIP(p, "deeper.network.device_gateway_ip", n.DeviceGatewayIP)
	if routerOK && gwOK && router == gw {
		p.addf("deeper.network.router_lan_ip and deeper.network.device_gateway_ip must differ (both are %s)", router)
	}
	if strings.TrimSpace(n.DeviceUpstreamIP) != "" {
		parseRequiredIP(p, "deeper.network.device_upstream_ip", n.DeviceUpstreamIP)
	}
	for _, w := range []struct{ field, v string }{
		{"deeper.network.deeper_wifi", n.DeeperWifi},
		{"deeper.network.upstream_wifi", n.UpstreamWifi},
	} {
		if isPlaceholder(w.v) {
			p.addf("%s is still the placeholder %q; fill it in or remove the key", w.field, w.v)
		}
	}
	for i, s := range n.FallbackWifi {
		if isPlaceholder(s) {
			p.addf("deeper.network.fallback_wifi[%d] is still the placeholder %q; fill it in or remove it", i, s)
		}
	}
	if isPlaceholder(d.DeepProbe.DNSTestDomain) {
		p.addf("deeper.deep_probe.dns_test_domain is still the placeholder %q; set a well-known domain", d.DeepProbe.DNSTestDomain)
	}

	// Failover.
	f := d.Failover
	known := map[string]bool{}
	for _, t := range KnownTiers {
		known[t] = true
	}
	for _, t := range f.TiersEnabled {
		if !known[t] {
			p.addf("deeper.failover.tiers_enabled contains unknown tier %q (known: %s)", t, strings.Join(KnownTiers, ", "))
		}
	}
	if f.Auto && len(f.TiersEnabled) == 0 {
		p.addf("deeper.failover.auto is true but tiers_enabled is empty; enable tiers cheapest-first, e.g. [t1]")
	}
	p.atLeast("deeper.failover.consecutive_failures", f.ConsecutiveFailures, 1)
	p.atLeast("deeper.failover.verify_cycles", f.VerifyCycles, 1)
	p.atLeast("deeper.failover.max_actions_per_hour", f.MaxActionsPerHour, 1)
}

func parseRequiredIP(p *problems, field, v string) (netip.Addr, bool) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		p.addf("%s is required with --deeper", field)
		return netip.Addr{}, false
	case isPlaceholder(v):
		p.addf("%s is still the placeholder %q; set a real IP address", field, v)
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(v)
	if err != nil {
		p.addf("%s %q is not a valid IP address", field, v)
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// Validate checks targets.yaml semantics. It needs the global thresholds
// because a per-target override is checked against the effective pair
// (e.g. a lone warn_ms must still be below the global crit).
func (t Targets) Validate(g Thresholds) []string {
	var p problems

	if len(t.Targets) == 0 {
		p.addf("targets: at least one target is required")
	}
	seen := map[string]int{}
	for i, tg := range t.Targets {
		label := fmt.Sprintf("targets[%d]", i)
		host := strings.TrimSpace(tg.Host)
		if host != "" {
			label = fmt.Sprintf("targets[%d] (%s)", i, host)
		}

		switch {
		case host == "":
			p.addf("%s: host must not be empty", label)
		case net.ParseIP(host) == nil && strings.ContainsAny(host, ": /\t\\@"):
			p.addf("%s: host must be a bare hostname or IP, without scheme, port or path", label)
		}
		if tg.Port < 1 || tg.Port > 65535 {
			p.addf("%s: port must be 1-65535 (got %d)", label, tg.Port)
		}

		if host != "" && tg.Port >= 1 && tg.Port <= 65535 {
			key := tg.Key()
			if first, dup := seen[key]; dup {
				p.addf("%s: duplicate target %s (same as targets[%d])", label, key, first)
			} else {
				seen[key] = i
			}
		}

		if tg.WarnMs < 0 || tg.CritMs < 0 {
			p.addf("%s: warn_ms and crit_ms must not be negative", label)
			continue
		}
		if tg.WarnMs > 0 || tg.CritMs > 0 {
			warn, crit := tg.EffectiveLatency(g)
			if warn >= crit {
				p.addf("%s: effective warn_ms (%v) must be less than crit_ms (%v); a lone override is compared with the global threshold", label, warn, crit)
			}
		}
	}
	return p
}
