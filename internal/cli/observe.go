package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/viveksbh/autoscope/internal/exitcode"
	"github.com/viveksbh/autoscope/internal/tcpdump"
)

func newObserveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Capture observability artifacts from a pod (tcpdump)",
	}
	cmd.AddCommand(newObserveTcpdumpCmd())
	return cmd
}

func newObserveTcpdumpCmd() *cobra.Command {
	var (
		targetNS  string
		container string
		filter    string
		duration  time.Duration
		snaplen   int
		iface     string
		outPath   string
		summary   bool
	)
	cmd := &cobra.Command{
		Use:   "tcpdump <pod>",
		Short: "Capture pcap from a target pod's network namespace",
		Long: `Spawns (or reuses) the privileged debug pod on the target pod's node, resolves the
workload PID via ctr, and runs tcpdump in the workload's net-ns. Stops on
duration elapse, Ctrl-C, target pod death, or debug pod death.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			podName := args[0]
			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}

			// Resolve target pod's node.
			pod, err := s.Kube.Clientset.CoreV1().Pods(targetNS).Get(ctx, podName, metaGetOpts())
			if err != nil {
				return exitcode.Wrap(exitcode.User, fmt.Errorf("get pod %s/%s: %w", targetNS, podName, err))
			}
			node := pod.Spec.NodeName
			if node == "" {
				return exitcode.Wrap(exitcode.User, fmt.Errorf("pod %s/%s not yet scheduled", targetNS, podName))
			}

			h, _, err := ensureDebugPod(ctx, s, node)
			if err != nil {
				return err
			}
			defer h.Close()

			durationSecs := int(duration / time.Second)

			deps := tcpdump.Deps{
				Clientset: s.Kube.Clientset,
				Config:    s.Kube.Config,
				Namespace: s.Globals.Namespace,
				Pod:       h.Name,
				Container: "auto",
				HandleCh:  h.Terminated,
			}
			opts := tcpdump.Opts{
				TargetPod:     podName,
				TargetNS:      targetNS,
				ContainerName: container,
				Filter:        filter,
				Iface:         iface,
				Snaplen:       snaplen,
				DurationSecs:  durationSecs,
				OutPath:       outPath,
				Summary:       summary,
				Stderr:        os.Stderr,
			}
			return tcpdump.Capture(ctx, deps, opts)
		},
	}
	cmd.Flags().StringVarP(&targetNS, "ns-target", "n", "default", "Target pod namespace")
	cmd.Flags().StringVar(&container, "container", "", "Target container name (required if multi-container)")
	cmd.Flags().StringVar(&filter, "filter", "", "BPF filter expression (e.g. 'port 80')")
	cmd.Flags().DurationVar(&duration, "duration", 30*time.Second, "Capture duration (0 = unlimited; Ctrl-C to stop)")
	cmd.Flags().IntVar(&snaplen, "snaplen", 262144, "tcpdump -s snapshot length")
	cmd.Flags().StringVar(&iface, "iface", "any", "Interface inside target net-ns")
	cmd.Flags().StringVar(&outPath, "out", "", "Output pcap path (default: ./tcpdump-<pod>-<ts>.pcap)")
	cmd.Flags().BoolVar(&summary, "summary", false, "Also print human-readable packet summary on stderr")
	return cmd
}
