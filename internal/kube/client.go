package kube

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Deps struct {
	Clientset *kubernetes.Clientset
	Config    *rest.Config
	Context   string
}

// LoadDeps loads kubeconfig from the given path/context and constructs a clientset.
// Empty kubeconfigPath falls back to $KUBECONFIG, then ~/.kube/config.
func LoadDeps(kubeconfigPath, contextName string) (*Deps, error) {
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}

	loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	// Load raw config once; derive REST config from it.
	raw, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}
	cc := clientcmd.NewDefaultClientConfig(*raw, overrides)
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("derive REST config from %s: %w", kubeconfigPath, err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("clientset: %w", err)
	}

	current := raw.CurrentContext
	if contextName != "" {
		current = contextName
	}

	return &Deps{Clientset: cs, Config: cfg, Context: current}, nil
}

// CheckClusterSuffix returns an error if `suffix` is non-empty and the
// active context name does not end with it. Use to guardrail against
// running against the wrong cluster.
func (d *Deps) CheckClusterSuffix(suffix string) error {
	if suffix == "" {
		return nil
	}
	if !strings.HasSuffix(d.Context, suffix) {
		return fmt.Errorf("context %q does not end with required suffix %q", d.Context, suffix)
	}
	return nil
}
