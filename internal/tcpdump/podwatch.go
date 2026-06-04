package tcpdump

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// WatchTargetPod watches the named pod from its current resourceVersion and
// returns a channel that closes when the pod transitions to Pending/Failed/
// Succeeded/Deleted, or when any container's restartCount increases above
// the value at first observation.
//
// resolveContainerPID is called once with the initial pod object before the
// watch loop begins; its return value is stashed alongside the channel.
func WatchTargetPod(
	ctx context.Context,
	cs kubernetes.Interface,
	ns, name string,
) (initial *corev1.Pod, terminated <-chan struct{}, err error) {
	initial, err = cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("get target pod %s/%s: %w", ns, name, err)
	}
	if initial.Status.Phase != corev1.PodRunning {
		return nil, nil, fmt.Errorf("target pod %s/%s phase=%s, want Running", ns, name, initial.Status.Phase)
	}

	w, err := cs.CoreV1().Pods(ns).Watch(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", name).String(),
		ResourceVersion: initial.ResourceVersion,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("watch target pod %s/%s: %w", ns, name, err)
	}

	baselineRestarts := totalRestarts(initial)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.ResultChan():
				if !ok {
					return
				}
				if ev.Type == watch.Deleted {
					return
				}
				p, isPod := ev.Object.(*corev1.Pod)
				if !isPod {
					continue
				}
				switch p.Status.Phase {
				case corev1.PodPending, corev1.PodFailed, corev1.PodSucceeded:
					return
				}
				if totalRestarts(p) > baselineRestarts {
					return
				}
			}
		}
	}()
	return initial, done, nil
}

func totalRestarts(p *corev1.Pod) int32 {
	var sum int32
	for _, cs := range p.Status.ContainerStatuses {
		sum += cs.RestartCount
	}
	return sum
}

// SelectContainer picks the workload container's status from the target pod.
// If named is non-empty, returns that exact container's status. Otherwise:
//   - 1 non-init container → that one.
//   - >1 non-init containers → error listing the names.
//
// init/ephemeral containers are excluded.
func SelectContainer(p *corev1.Pod, named string) (*corev1.ContainerStatus, error) {
	if named != "" {
		for i := range p.Status.ContainerStatuses {
			if p.Status.ContainerStatuses[i].Name == named {
				return &p.Status.ContainerStatuses[i], nil
			}
		}
		return nil, fmt.Errorf("container %q not found in pod %s/%s", named, p.Namespace, p.Name)
	}
	if len(p.Status.ContainerStatuses) == 1 {
		return &p.Status.ContainerStatuses[0], nil
	}
	names := make([]string, 0, len(p.Status.ContainerStatuses))
	for _, cs := range p.Status.ContainerStatuses {
		names = append(names, cs.Name)
	}
	return nil, fmt.Errorf("pod %s/%s has %d containers (%v); pass --container", p.Namespace, p.Name, len(names), names)
}
