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
	cre  map[string]*creEntry
}

type creEntry struct {
	once   sync.Once
	value  string
	err    error
	loaded bool
}

func NewNodeCache(d *Deps) *NodeCache {
	return &NodeCache{
		deps: d,
		cre:  make(map[string]*creEntry),
	}
}

func (n *NodeCache) entry(node string) *creEntry {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.cre[node]
	if !ok {
		e = &creEntry{}
		n.cre[node] = e
	}
	return e
}

// ContainerRuntimeEndpoint returns the host filesystem path of the containerd
// socket on the given node. Reads kubelet /configz via apiserver-proxy and
// strips the unix:// prefix. Falls back to DefaultRuntimeSocket on error,
// returning the error so callers can warn.
//
// Concurrent calls for the same node deduplicate via sync.Once; the first
// caller fetches, subsequent callers receive the cached value.
func (n *NodeCache) ContainerRuntimeEndpoint(ctx context.Context, node string) (string, error) {
	e := n.entry(node)
	e.once.Do(func() {
		e.value, e.err = n.fetchCRE(ctx, node)
		e.loaded = true
	})
	if !e.loaded {
		return DefaultRuntimeSocket, fmt.Errorf("nodecache: incomplete state for %s", node)
	}
	return e.value, e.err
}

func (n *NodeCache) fetchCRE(ctx context.Context, node string) (string, error) {
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
	return path, nil
}
