package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/viveksbh/autoscope/internal/debugpod"
	"github.com/viveksbh/autoscope/internal/exitcode"
)

func newExecCmd() *cobra.Command {
	var nsFlagsCSV string
	cmd := &cobra.Command{
		Use:   "exec <node> -- <cmd...>",
		Short: "Run a command in the host PID 1 namespaces",
		Long: `Spawns (or reuses) the privileged debug pod on <node> and runs <cmd...>
inside the host's mount/uts/ipc/pid namespaces (configurable via --ns).

Bottlerocket's host PID 1 shell rejects most utilities; argv[0] must be a
direct binary path (e.g. /usr/bin/journalctl). No /bin/sh -c wrapper.`,
		Args: cobra.MinimumNArgs(2), // <node> <cmd>...
		RunE: func(cmd *cobra.Command, args []string) error {
			node := args[0]
			rest := args[1:]
			if len(rest) == 0 {
				return exitcode.Wrap(exitcode.User, fmt.Errorf("missing command after node"))
			}

			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}

			h, sock, err := ensureDebugPod(ctx, s, node)
			if err != nil {
				return err
			}
			defer h.Close()
			_ = sock

			nsFlags := parseNSFlags(nsFlagsCSV)
			argv := append([]string{"nsenter", "-t", "1"}, nsFlags...)
			argv = append(argv, "--")
			argv = append(argv, rest...)

			fmt.Fprintln(os.Stderr, "[warn] running command in host PID 1 namespaces — root on node")

			return debugpod.ExecStream(ctx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name, debugpod.ExecOpts{
				Argv:   argv,
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
				Stderr: os.Stderr,
			})
		},
	}
	cmd.Flags().StringVar(&nsFlagsCSV, "ns", "mount,uts,ipc,pid", "Namespaces to enter (comma-list of mount,uts,ipc,net,pid,cgroup)")
	return cmd
}

func parseNSFlags(csv string) []string {
	mapping := map[string]string{
		"mount":  "-m",
		"uts":    "-u",
		"ipc":    "-i",
		"net":    "-n",
		"pid":    "-p",
		"cgroup": "-C",
	}
	out := make([]string, 0, 6)
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if f, ok := mapping[p]; ok {
			out = append(out, f)
		}
	}
	return out
}

// ensureDebugPod is the shared lazy-bootstrap path used by exec/logs/metrics/tcpdump.
// It calls EnsureNamespace + Ensure + returns the discovered runtime socket.
func ensureDebugPod(ctx context.Context, s *SessionDeps, node string) (*debugpod.Handle, string, error) {
	// Best-effort namespace bootstrap (no auto-label).
	if err := debugpod.EnsureNamespace(ctx, s.Kube.Clientset, s.Globals.Namespace, s.Caller, false); err != nil {
		return nil, "", err
	}
	// Opportunistic GC.
	_, _ = debugpod.SweepExpired(ctx, s.Kube.Clientset, s.Globals.Namespace)

	sock, err := s.NodeCache.ContainerRuntimeEndpoint(ctx, node)
	if err != nil && !s.Globals.Quiet {
		fmt.Fprintf(os.Stderr, "[warn] runtime endpoint discovery failed (%v); using fallback %s\n", err, sock)
	}

	sessionID := fmt.Sprintf("%s-%d", s.Caller, time.Now().Unix())

	h, err := debugpod.Ensure(ctx, s.Kube.Clientset, s.Globals.Namespace, node,
		s.Globals.Image, sock, sessionID, s.Caller, 90*time.Second)
	if err != nil {
		return nil, sock, err
	}
	if !s.Globals.Quiet {
		reused := "created"
		if h.Reused {
			reused = "reused"
		}
		fmt.Fprintf(os.Stderr, "[pod] %s %s on %s\n", reused, h.Name, node)
	}
	return h, sock, nil
}
