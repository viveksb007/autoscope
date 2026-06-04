package debugpod

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/viveksbh/autoscope/internal/exitcode"
)

// Handle is returned by Ensure: it identifies the running debug pod and
// signals when the pod stops being Running, so callers can abort streams.
type Handle struct {
	Name        string
	Namespace   string
	Node        string
	Reused      bool
	Terminated  <-chan struct{}
	cancelWatch context.CancelFunc
}

// Close stops the background watcher. Idempotent.
func (h *Handle) Close() {
	if h.cancelWatch != nil {
		h.cancelWatch()
	}
}

// Ensure returns a Handle to a Running debug pod for the given node.
// Deterministic name + AlreadyExists retry → no duplicate pods on race.
//
// Behavior:
//   - existing Running, TTL not expired → refresh TTL, reuse.
//   - existing Running, TTL expired → delete, recreate.
//   - existing Pending → wait for Running.
//   - existing terminal phase (Succeeded/Failed) → delete, recreate.
//   - absent → create.
func Ensure(
	ctx context.Context,
	cs kubernetes.Interface,
	ns, node, imageRef, runtimeSocket, sessionID, caller string,
	createTimeout time.Duration,
) (*Handle, error) {
	name := PodNameFor(node)

	for attempt := 0; attempt < 3; attempt++ {
		got, err := cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("get pod %s/%s: %w", ns, name, err))
		}

		switch {
		case apierrors.IsNotFound(err):
			// Create.
			spec := BuildPodSpec(node, ns, imageRef, runtimeSocket, sessionID, caller)
			created, cerr := cs.CoreV1().Pods(ns).Create(ctx, spec, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(cerr) {
				continue // race lost, re-fetch.
			}
			if cerr != nil {
				return nil, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("create pod %s/%s: %w", ns, name, cerr))
			}
			ready, werr := waitRunning(ctx, cs, ns, name, created.ResourceVersion, createTimeout)
			if werr != nil {
				return nil, werr
			}
			h, herr := startWatcher(ctx, cs, ns, name, ready.ResourceVersion)
			if herr != nil {
				return nil, herr
			}
			h.Node = node
			h.Reused = false
			return h, nil

		case got.Status.Phase == corev1.PodRunning:
			if isTTLExpired(got) {
				if derr := deleteAndWait(ctx, cs, ns, name); derr != nil {
					return nil, derr
				}
				continue
			}
			if perr := refreshTTL(ctx, cs, ns, name); perr != nil {
				return nil, perr
			}
			h, herr := startWatcher(ctx, cs, ns, name, got.ResourceVersion)
			if herr != nil {
				return nil, herr
			}
			h.Node = node
			h.Reused = true
			return h, nil

		case got.Status.Phase == corev1.PodPending:
			ready, werr := waitRunning(ctx, cs, ns, name, got.ResourceVersion, createTimeout)
			if werr != nil {
				return nil, werr
			}
			h, herr := startWatcher(ctx, cs, ns, name, ready.ResourceVersion)
			if herr != nil {
				return nil, herr
			}
			h.Node = node
			h.Reused = true
			return h, nil

		default:
			// Succeeded / Failed / Unknown — recycle.
			if derr := deleteAndWait(ctx, cs, ns, name); derr != nil {
				return nil, derr
			}
			continue
		}
	}

	return nil, exitcode.Wrap(exitcode.Internal,
		fmt.Errorf("ensure pod %s/%s: too many race retries", ns, name))
}

func isTTLExpired(p *corev1.Pod) bool {
	raw := p.Labels[LabelTTLEpoch]
	if raw == "" {
		return false
	}
	ttl, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() >= ttl
}

func refreshTTL(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	ttl := time.Now().Add(DefaultTTL).Unix()
	patch := []byte(fmt.Sprintf(
		`{"metadata":{"labels":{"%s":"%d"}}}`,
		LabelTTLEpoch, ttl))
	if _, err := cs.CoreV1().Pods(ns).Patch(
		ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{},
	); err != nil {
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("refresh TTL on %s/%s: %w", ns, name, err))
	}
	return nil
}

func deleteAndWait(ctx context.Context, cs kubernetes.Interface, ns, name string) error {
	zero := int64(0)
	if err := cs.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
	}); err != nil && !apierrors.IsNotFound(err) {
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("delete pod %s/%s: %w", ns, name, err))
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return exitcode.Wrap(exitcode.Sigint, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		_, err := cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
	}
	return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("delete pod %s/%s: timeout waiting gone", ns, name))
}

func waitRunning(
	ctx context.Context,
	cs kubernetes.Interface,
	ns, name, fromRV string,
	timeout time.Duration,
) (*corev1.Pod, error) {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	w, err := cs.CoreV1().Pods(ns).Watch(wctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", name).String(),
		ResourceVersion: fromRV,
	})
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("watch pod %s/%s: %w", ns, name, err))
	}
	defer w.Stop()

	for ev := range w.ResultChan() {
		if ev.Type == watch.Error {
			return nil, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("watch error on %s/%s", ns, name))
		}
		p, ok := ev.Object.(*corev1.Pod)
		if !ok {
			continue
		}
		switch p.Status.Phase {
		case corev1.PodRunning:
			return p, nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return nil, exitcode.Wrap(exitcode.Cluster,
				fmt.Errorf("pod %s/%s reached terminal phase %s before Running", ns, name, p.Status.Phase))
		}
	}
	if wctx.Err() == context.DeadlineExceeded {
		return nil, exitcode.Wrap(exitcode.Cluster,
			fmt.Errorf("pod %s/%s did not reach Running within %s", ns, name, timeout))
	}
	return nil, exitcode.Wrap(exitcode.Cluster,
		fmt.Errorf("watch closed unexpectedly for %s/%s", ns, name))
}

// startWatcher launches a background goroutine watching the pod from the
// given resourceVersion. The returned Handle's Terminated channel closes
// when the pod stops being Running.
func startWatcher(
	ctx context.Context,
	cs kubernetes.Interface,
	ns, name, fromRV string,
) (*Handle, error) {
	wctx, cancel := context.WithCancel(ctx)
	w, err := cs.CoreV1().Pods(ns).Watch(wctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", name).String(),
		ResourceVersion: fromRV,
	})
	if err != nil {
		cancel()
		return nil, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("watch pod %s/%s: %w", ns, name, err))
	}

	terminated := make(chan struct{})
	go func() {
		defer close(terminated)
		defer w.Stop()
		for {
			select {
			case <-wctx.Done():
				return
			case ev, ok := <-w.ResultChan():
				if !ok {
					return
				}
				p, isPod := ev.Object.(*corev1.Pod)
				if !isPod {
					continue
				}
				if ev.Type == watch.Deleted ||
					p.Status.Phase == corev1.PodFailed ||
					p.Status.Phase == corev1.PodSucceeded {
					return
				}
			}
		}
	}()

	return &Handle{
		Name:        name,
		Namespace:   ns,
		Terminated:  terminated,
		cancelWatch: cancel,
	}, nil
}
