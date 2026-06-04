// Package catalog holds the verified EKS-Auto agent catalog. Verified live
// on Bottlerocket EKS Auto Standard 2026.6.3 — see docs/VERIFY.md.
package catalog

// Transport selects how an endpoint is fetched.
type Transport int

const (
	// APIServerProxy: GET /api/v1/nodes/<node>/proxy/<path> (caller's RBAC).
	APIServerProxy Transport = iota
	// NodeLocalhost: nsenter -t 1 -n -- curl http://127.0.0.1:<port><path>
	// from the debug pod (uses pod's mount namespace for curl).
	NodeLocalhost
)

func (t Transport) String() string {
	switch t {
	case APIServerProxy:
		return "apiserver"
	case NodeLocalhost:
		return "node"
	default:
		return "unknown"
	}
}

// Endpoint describes a single named endpoint exposed by an agent.
type Endpoint struct {
	Name      string    // "metrics" | "healthz" | "ready" | "cadvisor" | etc.
	Transport Transport
	Port      int    // ignored for APIServerProxy
	Path      string // includes leading "/"
	Scheme    string // "http" (default) | "https"
}

// LogKind selects how a log source is read.
type LogKind int

const (
	// LogKindJournal: nsenter -t 1 -m -u -i -- /usr/bin/journalctl -u <Unit> ...
	LogKindJournal LogKind = iota
	// LogKindFile: nsenter -t 1 -m -u -i -- /usr/bin/tail [-n N | -F] <Path>
	LogKindFile
)

func (k LogKind) String() string {
	switch k {
	case LogKindJournal:
		return "journal"
	case LogKindFile:
		return "file"
	default:
		return "unknown"
	}
}

// LogSource is a single named log stream for an Agent.
type LogSource struct {
	Name  string  // catalog-local identifier, e.g. "policy", "bpf", "journal"
	Kind  LogKind
	Unit  string // for LogKindJournal
	Path  string // for LogKindFile (host filesystem path)
	Notes string
}

// Agent is a catalog entry for a known on-node systemd unit.
type Agent struct {
	Alias     string
	Unit      string // primary systemd unit name (used for backward-compat journal probe)
	Endpoints []Endpoint
	Logs      []LogSource // ordered: index 0 is the default `auto logs <agent>` source
	Notes     string
}

// Builtin is the live-verified catalog (docs/VERIFY.md round-2 + bonus probes).
var Builtin = []Agent{
	{
		Alias: "kubelet",
		Unit:  "kubelet.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: APIServerProxy, Path: "/metrics"},
			{Name: "cadvisor", Transport: APIServerProxy, Path: "/metrics/cadvisor"},
			{Name: "resource", Transport: APIServerProxy, Path: "/metrics/resource"},
			{Name: "healthz", Transport: NodeLocalhost, Port: 10248, Path: "/healthz", Scheme: "http"},
		},
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "kubelet.service", Notes: "kubelet stderr/stdout via journald"},
		},
		Notes: "Kubernetes kubelet",
	},
	{
		Alias:     "containerd",
		Unit:      "containerd.service",
		Endpoints: nil,
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "containerd.service", Notes: "containerd via journald"},
		},
		Notes: "Container runtime — no stable localhost endpoint; logs only",
	},
	{
		Alias: "kube-proxy",
		Unit:  "kube-proxy.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 10249, Path: "/metrics", Scheme: "http"},
			{Name: "healthz", Transport: NodeLocalhost, Port: 10256, Path: "/healthz", Scheme: "http"},
		},
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "kube-proxy.service"},
		},
		Notes: "Kubernetes kube-proxy",
	},
	{
		Alias: "ipamd",
		Unit:  "ipamd.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 61678, Path: "/metrics", Scheme: "http"},
			{Name: "introspect", Transport: NodeLocalhost, Port: 61679, Path: "/v1/enis", Scheme: "http"},
		},
		Logs: []LogSource{
			// File log first: structured ENI/IP allocation events.
			{Name: "ipamd", Kind: LogKindFile, Path: "/var/log/aws-routed-eni/ipamd.log",
				Notes: "structured ENI/IP allocation events"},
			{Name: "plugin", Kind: LogKindFile, Path: "/var/log/aws-routed-eni/plugin.log",
				Notes: "VPC CNI plugin per-pod ADD/DEL"},
			{Name: "egress-v6", Kind: LogKindFile, Path: "/var/log/aws-routed-eni/egress-v6-plugin.log",
				Notes: "egress-v6 plugin (IPv6 → IPv4 NAT)"},
			{Name: "journal", Kind: LogKindJournal, Unit: "ipamd.service",
				Notes: "ipamd stderr / fatal banners"},
		},
		Notes: "AWS VPC CNI IPAMD",
	},
	{
		Alias: "network-policy",
		Unit:  "aws-network-policy-agent.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 8900, Path: "/metrics", Scheme: "http"},
			{Name: "healthz", Transport: NodeLocalhost, Port: 8901, Path: "/healthz", Scheme: "http"},
		},
		Logs: []LogSource{
			// File log first: structured PolicyEndpoint reconcile + eBPF map programming.
			{Name: "policy", Kind: LogKindFile, Path: "/var/log/aws-routed-eni/network-policy-agent.log",
				Notes: "structured: PolicyEndpoint reconcile + eBPF map programming"},
			{Name: "bpf", Kind: LogKindFile, Path: "/var/log/aws-routed-eni/ebpf-sdk.log",
				Notes: "eBPF SDK: program load, map create, BTF resolve"},
			{Name: "journal", Kind: LogKindJournal, Unit: "aws-network-policy-agent.service",
				Notes: "stderr + embedded DNS proxy access log"},
		},
		Notes: "AWS Network Policy Agent",
	},
	{
		Alias: "node-monitor",
		Unit:  "eks-node-monitoring-agent.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 8801, Path: "/metrics", Scheme: "http"},
			{Name: "healthz", Transport: NodeLocalhost, Port: 8800, Path: "/healthz", Scheme: "http"},
		},
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "eks-node-monitoring-agent.service"},
		},
		Notes: "EKS Node Monitoring Agent",
	},
	{
		Alias: "pod-identity",
		Unit:  "eks-pod-identity-agent.service",
		Endpoints: []Endpoint{
			{Name: "healthz", Transport: NodeLocalhost, Port: 2703, Path: "/healthz", Scheme: "http"},
		},
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "eks-pod-identity-agent.service"},
		},
		Notes: "EKS Pod Identity Agent — no metrics endpoint",
	},
	{
		Alias: "coredns",
		Unit:  "coredns.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 9153, Path: "/metrics", Scheme: "http"},
			{Name: "health", Transport: NodeLocalhost, Port: 8080, Path: "/health", Scheme: "http"},
			{Name: "ready", Transport: NodeLocalhost, Port: 8181, Path: "/ready", Scheme: "http"},
		},
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "coredns.service"},
		},
		Notes: "CoreDNS",
	},
	{
		Alias:     "healthchecker",
		Unit:      "eks-healthchecker.service",
		Endpoints: nil,
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "eks-healthchecker.service"},
		},
		Notes: "EKS Auto health checker — logs only",
	},
	{
		Alias:     "ebs-csi",
		Unit:      "eks-ebs-csi-driver.service",
		Endpoints: nil,
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "eks-ebs-csi-driver.service"},
		},
		Notes: "EBS CSI node driver — logs only",
	},
	{
		Alias:     "ebs-csi-registrar",
		Unit:      "eks-ebs-csi-driver-registrar.service",
		Endpoints: nil,
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "eks-ebs-csi-driver-registrar.service"},
		},
		Notes: "EBS CSI Driver registrar — logs only",
	},
	{
		Alias:     "efa",
		Unit:      "efa-k8s-device-plugin.service",
		Endpoints: nil,
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: "efa-k8s-device-plugin.service"},
		},
		Notes: "EFA device plugin — logs only",
	},
}

// Lookup returns the catalog entry for alias, or a synthetic fallback
// {Unit: alias} so users can pass arbitrary unit names. The alias may be
// the catalog key (e.g. "kubelet") or the unit name itself
// (e.g. "kubelet.service").
func Lookup(alias string) Agent {
	for _, a := range Builtin {
		if a.Alias == alias || a.Unit == alias {
			return a
		}
	}
	// Fallback: pass-through. If user gave bare name, append .service.
	unit := alias
	if !hasDotService(alias) {
		unit = alias + ".service"
	}
	return Agent{
		Alias: alias,
		Unit:  unit,
		Logs: []LogSource{
			{Name: "journal", Kind: LogKindJournal, Unit: unit},
		},
	}
}

// FindEndpoint returns the named endpoint or (Endpoint{}, false).
func (a Agent) FindEndpoint(name string) (Endpoint, bool) {
	for _, e := range a.Endpoints {
		if e.Name == name {
			return e, true
		}
	}
	return Endpoint{}, false
}

// EndpointNames returns the list of endpoint names for error messages.
func (a Agent) EndpointNames() []string {
	out := make([]string, 0, len(a.Endpoints))
	for _, e := range a.Endpoints {
		out = append(out, e.Name)
	}
	return out
}

// FindLog returns the named log source or (LogSource{}, false).
func (a Agent) FindLog(name string) (LogSource, bool) {
	for _, l := range a.Logs {
		if l.Name == name {
			return l, true
		}
	}
	return LogSource{}, false
}

// LogNames returns the list of log source names (in priority order).
func (a Agent) LogNames() []string {
	out := make([]string, 0, len(a.Logs))
	for _, l := range a.Logs {
		out = append(out, l.Name)
	}
	return out
}

// DefaultLog returns the first log source (the catalog's recommended default).
// Falls back to a synthetic journal source built from a.Unit if Logs is empty.
func (a Agent) DefaultLog() LogSource {
	if len(a.Logs) > 0 {
		return a.Logs[0]
	}
	return LogSource{Name: "journal", Kind: LogKindJournal, Unit: a.Unit}
}

func hasDotService(s string) bool {
	const suffix = ".service"
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
