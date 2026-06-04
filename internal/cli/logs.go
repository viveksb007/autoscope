package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/viveksbh/autoscope/internal/catalog"
	"github.com/viveksbh/autoscope/internal/debugpod"
	"github.com/viveksbh/autoscope/internal/exitcode"
	"github.com/viveksbh/autoscope/internal/nsenter"
)

func newLogsCmd() *cobra.Command {
	var (
		tail   bool
		since  string
		lines  int
		grep   string
		noProbe bool
	)
	cmd := &cobra.Command{
		Use:               "logs <agent> <node>",
		Short:             "Stream host-systemd journal for an on-node unit",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeAgentsThenNodes,
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, node := args[0], args[1]
			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}

			h, _, err := ensureDebugPod(ctx, s, node)
			if err != nil {
				return err
			}
			defer h.Close()

			a := catalog.Lookup(alias)

			if !noProbe {
				if err := probeUnit(ctx, s, h, a.Unit); err != nil {
					return err
				}
			}

			flags := []string{}
			if tail {
				flags = append(flags, "-f")
			}
			if since != "" {
				flags = append(flags, "--since", since)
			}
			if !tail && lines > 0 {
				flags = append(flags, "-n", strconv.Itoa(lines))
			}

			argv := nsenter.JournalCtl(a.Unit, flags)

			out := os.Stdout
			var w io.Writer = out
			if grep != "" {
				re, err := regexp.Compile(grep)
				if err != nil {
					return exitcode.Wrap(exitcode.User, fmt.Errorf("invalid --grep regex: %w", err))
				}
				w = &grepWriter{re: re, dst: out}
			}

			return debugpod.ExecStream(ctx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name, debugpod.ExecOpts{
				Argv:   argv,
				Stdout: w,
				Stderr: os.Stderr,
			})
		},
	}
	cmd.Flags().BoolVarP(&tail, "tail", "f", false, "Follow (journalctl -f)")
	cmd.Flags().StringVar(&since, "since", "", "journalctl --since (e.g. 10m, 1h, RFC3339)")
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "Last N lines (ignored when --tail set)")
	cmd.Flags().StringVar(&grep, "grep", "", "Client-side regex filter applied to journalctl output")
	cmd.Flags().BoolVar(&noProbe, "no-probe", false, "Skip the unit-existence probe")
	return cmd
}

func probeUnit(ctx context.Context, s *SessionDeps, h *debugpod.Handle, unit string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := debugpod.ExecCapture(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name,
		nsenter.JournalUnitProbe(unit))
	if err != nil && exitcode.CodeOf(err) != exitcode.Node {
		// network/cluster error — surface
		return err
	}
	if len(out) > 0 {
		return nil
	}
	// Fallback: filesystem find via /etc/systemd/system + sys-root.
	// Detect arch lazily.
	archOut, _ := debugpod.ExecCapture(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name,
		[]string{"nsenter", "-t", "1", "-m", "-u", "-i", "--", "/usr/bin/uname", "-m"})
	arch := stripWS(string(archOut))
	if arch == "" {
		arch = "x86_64"
	}
	findOut, _ := debugpod.ExecCapture(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name,
		nsenter.FindSystemdUnitFile(unit, arch))
	if len(findOut) > 0 {
		return nil
	}
	return exitcode.Wrap(exitcode.Node, fmt.Errorf("systemd unit %q not present on node", unit))
}

func stripWS(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\r' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// grepWriter applies a regex line-filter to its destination.
// Buffers a partial trailing line across Write calls.
type grepWriter struct {
	re  *regexp.Regexp
	dst io.Writer
	buf []byte
}

func (g *grepWriter) Write(p []byte) (int, error) {
	g.buf = append(g.buf, p...)
	for {
		idx := -1
		for i, b := range g.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(g.buf[:idx])
		g.buf = g.buf[idx+1:]
		if g.re.MatchString(line) {
			if _, err := fmt.Fprintln(g.dst, line); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}
