package debugpod

import (
	"context"
	"errors"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/viveksb007/autoscope/internal/exitcode"
)

// ExecOpts captures the streams/argv for a single ExecStream call.
type ExecOpts struct {
	Argv   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	TTY    bool
}

// ExecStream runs argv inside the named container of pod ns/pod via SPDY exec.
// Cancellation of ctx closes the SPDY conn but does not deliver a remote signal —
// callers needing graceful flush should wrap argv with `timeout(1)` (busybox syntax).
func ExecStream(
	ctx context.Context,
	cs kubernetes.Interface,
	cfg *rest.Config,
	ns, pod string,
	opts ExecOpts,
) error {
	if len(opts.Argv) == 0 {
		return exitcode.Wrap(exitcode.Internal, fmt.Errorf("ExecStream: empty argv"))
	}

	req := cs.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   opts.Argv,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("spdy executor: %w", err))
	}

	streamOpts := remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	}

	if err := exec.StreamWithContext(ctx, streamOpts); err != nil {
		// SIGINT / ctx cancel takes priority over the remote error wrapping.
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return exitcode.Wrap(exitcode.Sigint, err)
		}
		// Map remote process exit-status errors to Node code.
		if _, ok := err.(utilexec.CodeExitError); ok {
			return exitcode.Wrap(exitcode.Node, err)
		}
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("exec stream: %w", err))
	}
	return nil
}

// ExecCapture runs argv and returns stdout as bytes. Stderr discarded.
// Convenience wrapper for short reads (ctr task list, journalctl probe, etc.).
func ExecCapture(
	ctx context.Context,
	cs kubernetes.Interface,
	cfg *rest.Config,
	ns, pod string,
	argv []string,
) ([]byte, error) {
	var stdout discardWriterBuf
	err := ExecStream(ctx, cs, cfg, ns, pod, ExecOpts{
		Argv:   argv,
		Stdout: &stdout,
		Stderr: io.Discard,
	})
	return stdout.Bytes(), err
}

type discardWriterBuf struct {
	buf []byte
}

func (d *discardWriterBuf) Write(p []byte) (int, error) {
	d.buf = append(d.buf, p...)
	return len(p), nil
}
func (d *discardWriterBuf) Bytes() []byte { return d.buf }
