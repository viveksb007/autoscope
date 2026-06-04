package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/viveksbh/autoscope/internal/debugpod"
	"github.com/viveksbh/autoscope/internal/exitcode"
)

func newCleanupCmd() *cobra.Command {
	var (
		node    string
		all     bool
		ttlOnly bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete debug pods",
		Long: `Delete debug pods. Default --all if neither --node nor --all is given.

Use --ttl-only for opportunistic GC: deletes only pods past their TTL annotation
or in terminal phase (Succeeded/Failed).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}

			if node == "" && !all && !ttlOnly {
				all = true
			}
			if node != "" && all {
				return exitcode.Wrap(exitcode.User, fmt.Errorf("--node and --all are mutually exclusive"))
			}

			if !s.Globals.Yes && !ttlOnly {
				if !confirm(fmt.Sprintf("Delete debug pods in namespace %s? [y/N] ", s.Globals.Namespace)) {
					return exitcode.Wrap(exitcode.User, fmt.Errorf("aborted"))
				}
			}

			deleted, err := debugpod.Cleanup(ctx, s.Kube.Clientset, s.Globals.Namespace, debugpod.CleanupFilter{
				Node:    node,
				TTLOnly: ttlOnly,
			})
			if err != nil {
				return err
			}
			for _, n := range deleted {
				fmt.Printf("deleted %s\n", n)
			}
			fmt.Printf("%d pod(s) deleted\n", len(deleted))
			return nil
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "Delete only the debug pod for this node")
	_ = cmd.RegisterFlagCompletionFunc("node", completeNodes)
	cmd.Flags().BoolVar(&all, "all", false, "Delete every debug pod in the namespace")
	cmd.Flags().BoolVar(&ttlOnly, "ttl-only", false, "Delete only TTL-expired or terminal pods")
	return cmd
}

func confirm(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}
