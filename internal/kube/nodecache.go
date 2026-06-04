package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// DefaultRuntimeSocket is the fallback when configz discovery fails.
const DefaultRuntimeSocket = "/run/containerd/containerd.sock"

// NodeCache caches per-node lookups (runtime socket, host arch).
type NodeCache struct {
	deps *Deps
	mu   sync.Mutex
	cre  map[string]string // node -> /path/to/runtime.sock
	arch map[string]string // node -> "x86_64" | "aarch64"
}

func NewNodeCache(d *Deps) *NodeCache {
	return &NodeCache{
		deps: d,
		cre:  make(map[string]string),
		arch: make(map[string]string),
	}
}

// ContainerRuntimeEndpoint returns the host filesystem path of the containerd
// socket on the given node. Reads kubelet /configz via apiserver-proxy and
// strips the unix:// prefix. Falls back to DefaultRuntimeSocket on error,
// returning the error so callers can warn.
func (n *NodeCache) ContainerRuntimeEndpoint(ctx context.Context, node string) (string, error) {
	n.mu.Lock()
	if v, ok := n.cre[node]; ok {
		n.mu.Unlock()
		return v, nil
	}
	n.mu.Unlock()

	raw, err := n.deps.Clientset.RESTClient().
		Get().
		AbsPath(fmt.Sprintf("/api/v1/nodes/%s/proxy/configz", node)).
		DoRaw(ctx)
	if err != nil {
		return DefaultRuntimeSocket, fmt.Errorf("configz fetch for %s: %w", node, err)
	}

	var cfg struct {
		Kubeletconfig struct {
			ContainerRuntimeEndpoint string `json:"containerRuntimeEndpoint"`
		} `json:"kubeletconfig"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultRuntimeSocket, fmt.Errorf("configz parse for %s: %w", node, err)
	}

	path := strings.TrimPrefix(cfg.Kubeletconfig.ContainerRuntimeEndpoint, "unix://")
	if path == "" {
		path = DefaultRuntimeSocket
	}

	n.mu.Lock()
	n.cre[node] = path
	n.mu.Unlock()
	return path, nil
}
