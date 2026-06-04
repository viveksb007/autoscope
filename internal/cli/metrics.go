package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/viveksbh/autoscope/internal/catalog"
	"github.com/viveksbh/autoscope/internal/debugpod"
	"github.com/viveksbh/autoscope/internal/exitcode"
	"github.com/viveksbh/autoscope/internal/nsenter"
)

func newMetricsCmd() *cobra.Command {
	var (
		endpoint string
		port     int
		path     string
		via      string
		tail     time.Duration
	)
	cmd := &cobra.Command{
		Use:               "metrics <agent> <node>",
		Short:             "Pull Prometheus metrics or healthz from an on-node agent",
		ValidArgsFunction: completeAgentsThenNodes,
		Long: `Resolution order (per docs/TOOL.md):
  1. --port AND --path set        -> node transport, ignore catalog
  2. --port XOR --path             -> exit 1
  3. --endpoint NAME matches catalog -> use that endpoint
  4. --endpoint not set, agent has 'metrics' -> use it
  5. --endpoint not set, agent has no metrics -> exit 1 (lists alternatives)
  6. --endpoint set, no match       -> exit 1`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, node := args[0], args[1]
			ctx := cmd.Context()
			s, err := LoadSession(ctx)
			if err != nil {
				return err
			}

			ep, err := resolveEndpoint(alias, endpoint, port, path, via)
			if err != nil {
				return err
			}

			fetch := func() error {
				return fetchOnce(ctx, s, node, ep)
			}

			if tail == 0 {
				return fetch()
			}
			t := time.NewTicker(tail)
			defer t.Stop()
			if err := fetch(); err != nil {
				return err
			}
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-t.C:
					if err := fetch(); err != nil {
						return err
					}
				}
			}
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Catalog endpoint name (default: metrics)")
	cmd.Flags().IntVar(&port, "port", 0, "Override port (requires --path)")
	cmd.Flags().StringVar(&path, "path", "", "Override path (requires --port)")
	cmd.Flags().StringVar(&via, "via", "", "Force transport: apiserver | node")
	cmd.Flags().DurationVar(&tail, "tail", 0, "Repeat fetch every interval (e.g. 20s); Ctrl-C to stop")
	return cmd
}

func resolveEndpoint(alias, endpoint string, port int, path, via string) (catalog.Endpoint, error) {
	// Step 1: explicit port+path bypass catalog.
	if port != 0 && path != "" {
		ep := catalog.Endpoint{Name: "custom", Transport: catalog.NodeLocalhost, Port: port, Path: path, Scheme: "http"}
		if v, err := parseVia(via); err == nil && v != nil {
			ep.Transport = *v
		}
		return ep, nil
	}
	// Step 2: half-set port/path.
	if (port != 0) != (path != "") {
		return catalog.Endpoint{}, exitcode.Wrap(exitcode.User,
			fmt.Errorf("--port and --path must both be set or both unset"))
	}

	a := catalog.Lookup(alias)

	// Step 3 / 4 / 5 / 6.
	name := endpoint
	if name == "" {
		name = "metrics"
	}
	ep, ok := a.FindEndpoint(name)
	if !ok {
		if endpoint == "" {
			// case 5
			return catalog.Endpoint{}, exitcode.Wrap(exitcode.User,
				fmt.Errorf("agent %q has no 'metrics' endpoint; available: %s", alias, formatList(a.EndpointNames())))
		}
		// case 6
		return catalog.Endpoint{}, exitcode.Wrap(exitcode.User,
			fmt.Errorf("agent %q has no endpoint named %q; available: %s. Use --port/--path for arbitrary endpoints",
				alias, endpoint, formatList(a.EndpointNames())))
	}
	if v, err := parseVia(via); err == nil && v != nil {
		ep.Transport = *v
	} else if err != nil {
		return catalog.Endpoint{}, err
	}
	return ep, nil
}

func parseVia(v string) (*catalog.Transport, error) {
	switch v {
	case "":
		return nil, nil
	case "apiserver":
		t := catalog.APIServerProxy
		return &t, nil
	case "node":
		t := catalog.NodeLocalhost
		return &t, nil
	default:
		return nil, exitcode.Wrap(exitcode.User, fmt.Errorf("--via must be 'apiserver' or 'node', got %q", v))
	}
}

func formatList(xs []string) string {
	if len(xs) == 0 {
		return "(none)"
	}
	return strings.Join(xs, ", ")
}

func fetchOnce(ctx context.Context, s *SessionDeps, node string, ep catalog.Endpoint) error {
	switch ep.Transport {
	case catalog.APIServerProxy:
		return fetchAPIServerProxy(ctx, s, node, ep)
	case catalog.NodeLocalhost:
		return fetchNodeLocalhost(ctx, s, node, ep)
	default:
		return exitcode.Wrap(exitcode.Internal, fmt.Errorf("unknown transport %d", ep.Transport))
	}
}

func fetchAPIServerProxy(ctx context.Context, s *SessionDeps, node string, ep catalog.Endpoint) error {
	abs := fmt.Sprintf("/api/v1/nodes/%s/proxy%s", node, ep.Path)
	raw, err := s.Kube.Clientset.RESTClient().Get().AbsPath(abs).DoRaw(ctx)
	if err != nil {
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("apiserver-proxy GET %s: %w", abs, err))
	}
	if _, err := os.Stdout.Write(raw); err != nil {
		return exitcode.Wrap(exitcode.Internal, err)
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func fetchNodeLocalhost(ctx context.Context, s *SessionDeps, node string, ep catalog.Endpoint) error {
	h, _, err := ensureDebugPod(ctx, s, node)
	if err != nil {
		return err
	}
	defer h.Close()

	scheme := ep.Scheme
	if scheme == "" {
		scheme = "http"
	}
	argv := nsenter.CurlLocalhost(scheme, ep.Port, ep.Path, 5)

	return debugpod.ExecStream(ctx, s.Kube.Clientset, s.Kube.Config, s.Globals.Namespace, h.Name, debugpod.ExecOpts{
		Argv:   argv,
		Stdout: os.Stdout,
		Stderr: io.Discard,
	})
}
