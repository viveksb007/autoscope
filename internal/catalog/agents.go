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

// Agent is a catalog entry for a known on-node systemd unit.
type Agent struct {
	Alias     string
	Unit      string // systemd unit name, e.g. "kubelet.service"
	Endpoints []Endpoint
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
		Notes: "Kubernetes kubelet",
	},
	{
		Alias:     "containerd",
		Unit:      "containerd.service",
		Endpoints: nil,
		Notes:     "Container runtime — no stable localhost endpoint; logs only",
	},
	{
		Alias: "kube-proxy",
		Unit:  "kube-proxy.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 10249, Path: "/metrics", Scheme: "http"},
			{Name: "healthz", Transport: NodeLocalhost, Port: 10256, Path: "/healthz", Scheme: "http"},
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
		Notes: "AWS VPC CNI IPAMD",
	},
	{
		Alias: "network-policy",
		Unit:  "aws-network-policy-agent.service",
		Endpoints: []Endpoint{
			{Name: "metrics", Transport: NodeLocalhost, Port: 8900, Path: "/metrics", Scheme: "http"},
			{Name: "healthz", Transport: NodeLocalhost, Port: 8901, Path: "/healthz", Scheme: "http"},
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
		Notes: "EKS Node Monitoring Agent",
	},
	{
		Alias: "pod-identity",
		Unit:  "eks-pod-identity-agent.service",
		Endpoints: []Endpoint{
			{Name: "healthz", Transport: NodeLocalhost, Port: 2703, Path: "/healthz", Scheme: "http"},
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
		Notes: "CoreDNS",
	},
	{
		Alias:     "healthchecker",
		Unit:      "eks-healthchecker.service",
		Endpoints: nil,
		Notes:     "EKS Auto health checker — logs only",
	},
	{
		Alias:     "ebs-csi",
		Unit:      "eks-ebs-csi-driver.service",
		Endpoints: nil,
		Notes:     "EBS CSI node driver — logs only",
	},
	{
		Alias:     "ebs-csi-registrar",
		Unit:      "eks-ebs-csi-driver-registrar.service",
		Endpoints: nil,
		Notes:     "EBS CSI Driver registrar — logs only",
	},
	{
		Alias:     "efa",
		Unit:      "efa-k8s-device-plugin.service",
		Endpoints: nil,
		Notes:     "EFA device plugin — logs only",
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
	return Agent{Alias: alias, Unit: unit}
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

func hasDotService(s string) bool {
	const suffix = ".service"
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
