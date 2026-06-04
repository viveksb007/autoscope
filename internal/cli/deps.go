package cli

import (
	"context"
	"fmt"
	"os/user"

	"github.com/viveksbh/autoscope/internal/exitcode"
	"github.com/viveksbh/autoscope/internal/kube"
)

// SessionDeps bundles the per-invocation runtime objects every subcommand uses.
type SessionDeps struct {
	Globals   *GlobalFlags
	Kube      *kube.Deps
	NodeCache *kube.NodeCache
	Caller    string
}

// LoadSession constructs the per-command runtime, applies guardrails
// (cluster-suffix gate), and returns a SessionDeps ready for use.
func LoadSession(ctx context.Context) (*SessionDeps, error) {
	g := FlagsFrom(ctx)
	if g == nil {
		return nil, exitcode.Wrap(exitcode.Internal, fmt.Errorf("globals not stashed on context"))
	}
	d, err := kube.LoadDeps(g.Kubeconfig, g.Context)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Cluster, err)
	}
	if err := d.CheckClusterSuffix(g.RequireClusterSuffix); err != nil {
		return nil, exitcode.Wrap(exitcode.User, err)
	}

	caller := "unknown"
	if u, err := user.Current(); err == nil {
		caller = u.Username
	}

	return &SessionDeps{
		Globals:   g,
		Kube:      d,
		NodeCache: kube.NewNodeCache(d),
		Caller:    caller,
	}, nil
}
