package cli

import (
	"github.com/spf13/cobra"
)

// Build-time vars (set via -ldflags in Phase 7).
var (
	Version     = "v0.1.0-dev"
	ImageDigest = "nicolaka/netshoot@sha256:0000000000000000000000000000000000000000000000000000000000000000"
)

// Global flags.
type globalFlags struct {
	Kubeconfig            string
	Context               string
	Namespace             string
	Image                 string
	JSON                  bool
	Quiet                 bool
	Verbose              bool
	Yes                   bool
	RequireClusterSuffix string
}

func NewRootCmd() *cobra.Command {
	g := &globalFlags{}
	cmd := &cobra.Command{
		Use:   "auto",
		Short: "On-node debugger for EKS Auto Mode (Bottlerocket)",
		Long: `auto creates an on-demand privileged pod on a target node and exposes
tcpdump, journalctl, host-localhost metrics, and arbitrary host-PID-1 commands
through a simple subcommand surface. See docs/TOOL.md for full contract.`,
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVar(&g.Kubeconfig, "kubeconfig", "", "Path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
	cmd.PersistentFlags().StringVar(&g.Context, "context", "", "Kubeconfig context to use")
	cmd.PersistentFlags().StringVarP(&g.Namespace, "namespace", "N", "auto-debug", "Namespace where debug pods run")
	cmd.PersistentFlags().StringVar(&g.Image, "image", ImageDigest, "Debug pod image (digest-pinned)")
	cmd.PersistentFlags().BoolVar(&g.JSON, "json", false, "Emit NDJSON output")
	cmd.PersistentFlags().BoolVar(&g.Quiet, "quiet", false, "Suppress informational stderr")
	cmd.PersistentFlags().BoolVarP(&g.Verbose, "verbose", "v", false, "Verbose stderr")
	cmd.PersistentFlags().BoolVar(&g.Yes, "yes", false, "Skip confirmation prompts")
	cmd.PersistentFlags().StringVar(&g.RequireClusterSuffix, "require-cluster-suffix", "", "Refuse to operate unless current context name has this suffix")

	cmd.AddCommand(newVersionCmd())

	return cmd
}
