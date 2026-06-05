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

	"github.com/viveksb007/autoscope/internal/catalog"
	"github.com/viveksb007/autoscope/internal/debugpod"
	"github.com/viveksb007/autoscope/internal/exitcode"
	"github.com/viveksb007/autoscope/internal/nsenter"
)

func newLogsCmd() *cobra.Command {
	var (
		tail    bool
		since   string
		lines   int
		grep    string
		noProbe bool
		source  string
	)
	cmd := &cobra.Command{
		Use:   "logs <agent> <node>",
		Short: "Stream logs from an on-node agent (systemd journal or file)",
		Long: `Selects the agent's default log source (file or journald) from the catalog.
Override with --source NAME. List available sources with --source list.

Sources by agent (default first):
  network-policy : policy (file), bpf (file), journal
  ipamd          : ipamd (file), plugin (file), egress-v6 (file), journal
  kubelet, containerd, kube-proxy, ... : journal`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeAgentsThenNodes,
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, node := args[0], args[1]
			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}

			a := catalog.Lookup(alias)

			// `--source list` prints available sources without spawning the debug pod.
			if source == "list" {
				if len(a.Logs) == 0 {
					fmt.Printf("agent %q has no log sources defined\n", alias)
					return nil
				}
				for i, l := range a.Logs {
					def := ""
					if i == 0 {
						def = " (default)"
					}
					switch l.Kind {
					case catalog.LogKindFile:
						fmt.Printf("  %-12s file     %s%s\n", l.Name, l.Path, def)
					case catalog.LogKindJournal:
						fmt.Printf("  %-12s journal  %s%s\n", l.Name, l.Unit, def)
					}
					if l.Notes != "" {
						fmt.Printf("  %-12s          %s\n", "", l.Notes)
					}
				}
				return nil
			}

			ls, err := resolveLogSource(a, source)
			if err != nil {
				return err
			}

			h, _, err := ensureDebugPod(ctx, s, node)
			if err != nil {
				return err
			}
			defer h.Close()

			if !noProbe {
				if err := probeLogSource(ctx, s, h, ls); err != nil {
					return err
				}
			}

			argv := buildLogArgv(ls, tail, since, lines)

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
	cmd.Flags().BoolVarP(&tail, "tail", "f", false, "Follow (journalctl -f / tail -F)")
	cmd.Flags().StringVar(&since, "since", "", "journalctl --since (journal sources only; e.g. 10m, 1h, RFC3339)")
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "Last N lines (ignored when --tail set)")
	cmd.Flags().StringVar(&grep, "grep", "", "Client-side regex filter applied to log output")
	cmd.Flags().BoolVar(&noProbe, "no-probe", false, "Skip the source-existence probe")
	cmd.Flags().StringVar(&source, "source", "", "Catalog source name (default: first source for the agent). Use 'list' to enumerate.")
	return cmd
}

// resolveLogSource picks a LogSource from the catalog entry per the rules:
//   - source == ""   ⇒ a.DefaultLog() (first entry, or synthetic journal for unknown alias)
//   - source matches a.FindLog ⇒ that source
//   - source == "journal" but agent has no journal entry ⇒ synthesize from a.Unit
//   - else ⇒ exit code 1 listing available names
func resolveLogSource(a catalog.Agent, source string) (catalog.LogSource, error) {
	if source == "" {
		return a.DefaultLog(), nil
	}
	if ls, ok := a.FindLog(source); ok {
		return ls, nil
	}
	if source == "journal" && a.Unit != "" {
		return catalog.LogSource{Name: "journal", Kind: catalog.LogKindJournal, Unit: a.Unit}, nil
	}
	return catalog.LogSource{}, exitcode.Wrap(exitcode.User,
		fmt.Errorf("agent %q has no log source named %q; available: %s. Use --source list to inspect.",
			a.Alias, source, formatList(a.LogNames())))
}

// buildLogArgv constructs the remote command argv for the chosen LogSource.
// Flag semantics (per docs/TOOL.md): --tail = follow; --since = journal-only;
// --lines = N for both kinds.
func buildLogArgv(ls catalog.LogSource, tail bool, since string, lines int) []string {
	switch ls.Kind {
	case catalog.LogKindFile:
		// File: use tail. --since is silently ignored (no equivalent for tail).
		n := lines
		if tail {
			n = 0 // -F default = no -n; user typically wants live tail to start at end
		}
		return nsenter.TailFile(ls.Path, tail, n)
	case catalog.LogKindJournal:
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
		return nsenter.JournalCtl(ls.Unit, flags)
	default:
		return nil
	}
}

// probeLogSource ensures the chosen source actually exists on the node:
//   - File: `test -f <path>` exit code.
//   - Journal: byte-count probe (verified in docs/VERIFY.md round-2) with
//     filesystem-find fallback for units that haven't logged anything yet.
func probeLogSource(ctx context.Context, s *SessionDeps, h *debugpod.Handle, ls catalog.LogSource) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	switch ls.Kind {
	case catalog.LogKindFile:
		err := debugpod.ExecStream(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name, debugpod.ExecOpts{
			Argv:   nsenter.FileExistsProbe(ls.Path),
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
		if err == nil {
			return nil
		}
		if exitcode.CodeOf(err) == exitcode.Node {
			return exitcode.Wrap(exitcode.Node,
				fmt.Errorf("log file %q not present on node (source=%q)", ls.Path, ls.Name))
		}
		return err

	case catalog.LogKindJournal:
		out, err := debugpod.ExecCapture(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name,
			nsenter.JournalUnitProbe(ls.Unit))
		if err != nil && exitcode.CodeOf(err) != exitcode.Node {
			return err
		}
		if len(out) > 0 {
			return nil
		}
		archOut, _ := debugpod.ExecCapture(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name,
			nsenter.HostMount("/usr/bin/uname", "-m"))
		arch := stripWS(string(archOut))
		if arch == "" {
			arch = "x86_64"
		}
		findOut, _ := debugpod.ExecCapture(probeCtx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name,
			nsenter.FindSystemdUnitFile(ls.Unit, arch))
		if len(findOut) > 0 {
			return nil
		}
		return exitcode.Wrap(exitcode.Node, fmt.Errorf("systemd unit %q not present on node", ls.Unit))
	}
	return nil
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
