package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/viveksbh/autoscope/internal/debugpod"
)

func newInstallCmd() *cobra.Command {
	var autoLabel bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Bootstrap the auto-debug namespace (PSA: privileged)",
		Long: `Idempotent state machine:
  - namespace absent       → create with PSA-privileged label
  - namespace present + OK → no-op
  - namespace present − OK + --auto-label → patch label
  - namespace present − OK + no flag       → refuse (exit 1)

See docs/TOOL.md "Cluster Bootstrap".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}
			if err := debugpod.EnsureNamespace(ctx, s.Kube.Clientset, s.Globals.Namespace, s.Caller, autoLabel); err != nil {
				return err
			}
			fmt.Printf("namespace %s ready\n", s.Globals.Namespace)
			return nil
		},
	}
	cmd.Flags().BoolVar(&autoLabel, "auto-label", false, "Patch existing namespace if it lacks the PSA-privileged label")
	return cmd
}
