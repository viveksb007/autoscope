package kube

import (
	"fmt"
	"os"
	"path/filepath"

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
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("clientset: %w", err)
	}

	resolved, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides).RawConfig()
	if err != nil {
		return nil, fmt.Errorf("raw kubeconfig: %w", err)
	}
	current := resolved.CurrentContext
	if contextName != "" {
		current = contextName
	}

	return &Deps{Clientset: cs, Config: cfg, Context: current}, nil
}
