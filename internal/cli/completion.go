package cli

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/viveksb007/autoscope/internal/catalog"
	"github.com/viveksb007/autoscope/internal/kube"
)

// completeAgents returns catalog aliases as completion candidates with
// short descriptions. Used as ValidArgsFunction for the <agent> positional
// in `auto logs` and `auto metrics`.
func completeAgents(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]cobra.Completion, 0, len(catalog.Builtin))
	for _, a := range catalog.Builtin {
		desc := a.Notes
		if desc == "" {
			desc = a.Unit
		}
		out = append(out, a.Alias+"\t"+desc)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeNodes lists the cluster's node names.
//
// Falls back to no-completion if kubeconfig load or the apiserver call fails,
// so a slow/unavailable cluster doesn't break tab-completion outright.
func completeNodes(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Read --kubeconfig / --context off the cmd if set, else default env.
	kubeconfig, _ := cmd.Flags().GetString("kubeconfig")
	contextName, _ := cmd.Flags().GetString("context")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	d, err := kube.LoadDeps(kubeconfig, contextName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	nodes, err := d.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]cobra.Completion, 0, len(nodes.Items))
	for i := range nodes.Items {
		out = append(out, nodes.Items[i].Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeAgentsThenNodes wires two-positional commands like
// `auto logs <agent> <node>` and `auto metrics <agent> <node>`.
func completeAgentsThenNodes(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeAgents(cmd, args, toComplete)
	case 1:
		return completeNodes(cmd, args, toComplete)
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeNodesOnly is for commands where the first positional is a node
// (e.g. `auto exec <node>`).
func completeNodesOnly(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeNodes(cmd, args, toComplete)
	}
	return nil, cobra.ShellCompDirectiveDefault
}
