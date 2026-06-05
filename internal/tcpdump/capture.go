// Package tcpdump orchestrates pcap capture from a target pod's network
// namespace via the privileged debug pod. See docs/DESIGN.md "observe tcpdump".
package tcpdump

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/viveksb007/autoscope/internal/debugpod"
	"github.com/viveksb007/autoscope/internal/exitcode"
	"github.com/viveksb007/autoscope/internal/nsenter"
)

// Opts captures user-supplied tcpdump configuration.
type Opts struct {
	TargetPod     string
	TargetNS      string
	ContainerName string // empty = first non-init, error if ambiguous
	Filter        string // BPF expression; empty = capture all
	Iface         string // default: "any"
	Snaplen       int    // default: 262144
	DurationSecs  int    // 0 = unlimited (no busybox timeout wrapper)
	OutPath       string // pcap destination; empty = ./tcpdump-<pod>-<ts>.pcap
	Summary       bool   // enable second tcpdump for human-readable summary
	Stderr        io.Writer
}

// Deps bundles the kube + debug-pod handles needed to run a capture.
type Deps struct {
	Clientset kubernetes.Interface
	Config    *rest.Config
	Namespace string  // namespace where debug pod runs
	Pod       string  // debug pod name
	Container string  // debug pod container name (defaults to "auto")
	HandleCh  <-chan struct{} // closed when debug pod terminates
}

// Capture orchestrates the pcap stream end-to-end:
//   1. Locate target pod, pick container.
//   2. Resolve workload PID via `ctr -n k8s.io tasks list`.
//   3. Watch target pod (resourceVersion-anchored) for death/restart.
//   4. errgroup of one (or two if --summary) tcpdump streams.
//   5. Cancel on duration, Ctrl-C, target-pod death, debug-pod death.
//   6. fsync + close pcap file regardless of outcome.
func Capture(ctx context.Context, d Deps, o Opts) error {
	if o.Iface == "" {
		o.Iface = "any"
	}
	if o.Snaplen == 0 {
		o.Snaplen = 262144
	}

	target, targetTerminated, err := WatchTargetPod(ctx, d.Clientset, o.TargetNS, o.TargetPod)
	if err != nil {
		return exitcode.Wrap(exitcode.User, err)
	}
	cs, err := SelectContainer(target, o.ContainerName)
	if err != nil {
		return exitcode.Wrap(exitcode.User, err)
	}
	containerID := nsenter.StripContainerIDPrefix(cs.ContainerID)
	if containerID == "" {
		return exitcode.Wrap(exitcode.Node, fmt.Errorf("container %s in %s/%s has no containerID yet", cs.Name, o.TargetNS, o.TargetPod))
	}

	pid, err := resolveWorkloadPID(ctx, d, containerID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderrOr(o.Stderr), "[tcpdump] target pid=%d container=%s pod=%s/%s\n",
		pid, cs.Name, o.TargetNS, o.TargetPod)

	outPath := o.OutPath
	if outPath == "" {
		outPath = defaultOutPath(o.TargetPod)
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return exitcode.Wrap(exitcode.Internal, fmt.Errorf("open pcap file %s: %w", outPath, err))
	}
	defer func() {
		_ = f.Sync()
		_ = f.Close()
	}()

	// Combine ctx with target-pod-watcher and debug-pod-watcher.
	captureCtx, cancel := context.WithCancel(ctx)
	fanDone := make(chan struct{})
	defer func() {
		cancel()
		<-fanDone // ensure fan-out goroutine has exited before returning
	}()
	go func() {
		defer close(fanDone)
		select {
		case <-captureCtx.Done():
		case <-targetTerminated:
			fmt.Fprintln(stderrOr(o.Stderr), "[tcpdump] target pod terminated; stopping capture")
			cancel()
		case <-d.HandleCh:
			fmt.Fprintln(stderrOr(o.Stderr), "[tcpdump] debug pod terminated; stopping capture")
			cancel()
		}
	}()

	g, gctx := errgroup.WithContext(captureCtx)

	// Raw pcap stream → file.
	g.Go(func() error {
		argv := nsenter.TcpdumpRaw(pid, o.Iface, o.Snaplen, o.Filter, o.DurationSecs)
		return debugpod.ExecStream(gctx, d.Clientset, d.Config, d.Namespace, d.Pod, debugpod.ExecOpts{
			Argv:   argv,
			Stdout: f,
			Stderr: stderrOr(o.Stderr),
		})
	})

	// Optional summary stream → stderr.
	if o.Summary {
		g.Go(func() error {
			argv := nsenter.TcpdumpSummary(pid, o.Iface, 0, o.Filter, o.DurationSecs)
			return debugpod.ExecStream(gctx, d.Clientset, d.Config, d.Namespace, d.Pod, debugpod.ExecOpts{
				Argv:   argv,
				Stdout: stderrOr(o.Stderr),
				Stderr: io.Discard,
			})
		})
	}

	gErr := g.Wait()

	// busybox timeout exits 143 (SIGTERM) or 124 (timeout) when duration cap hits.
	// Treat these as success — they're how the duration loop ends.
	if isExpectedTimeoutExit(gErr) || errors.Is(gErr, context.Canceled) {
		gErr = nil
	}
	if gErr != nil {
		return gErr
	}
	fmt.Fprintf(stderrOr(o.Stderr), "[tcpdump] saved %s\n", outPath)
	return nil
}

func isExpectedTimeoutExit(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ExitStatus() int }
	var c coder
	if errors.As(err, &c) {
		switch c.ExitStatus() {
		case 124, 143: // busybox timeout / SIGTERM via -k
			return true
		}
	}
	return false
}

// resolveWorkloadPID runs `ctr -n k8s.io tasks list` in the debug pod and
// parses the workload's PID from the output.
func resolveWorkloadPID(ctx context.Context, d Deps, containerID string) (int, error) {
	out, err := debugpod.ExecCapture(ctx, d.Clientset, d.Config, d.Namespace, d.Pod, nsenter.CtrTaskList())
	if err != nil {
		return 0, err
	}
	pid, err := nsenter.FindPID(out, containerID)
	if err != nil {
		return 0, exitcode.Wrap(exitcode.Node, err)
	}
	return pid, nil
}

func defaultOutPath(pod string) string {
	ts := nowStamp()
	return fmt.Sprintf("tcpdump-%s-%s.pcap", pod, ts)
}

// nowStamp returns a filename-safe timestamp; overridable in tests.
var nowStamp = func() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func stderrOr(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stderr
}
