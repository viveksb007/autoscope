package debugpod

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/viveksbh/autoscope/internal/exitcode"
)

// CleanupFilter narrows which debug pods to delete.
type CleanupFilter struct {
	Node    string // empty = any
	TTLOnly bool   // true: only TTL-expired or terminal pods
}

// Cleanup deletes debug pods matching the filter. Returns the names deleted.
func Cleanup(ctx context.Context, cs kubernetes.Interface, ns string, f CleanupFilter) ([]string, error) {
	selector := LabelSession // existence selector via label key

	listOpts := metav1.ListOptions{
		LabelSelector: selector,
	}
	if f.Node != "" {
		listOpts.LabelSelector = fmt.Sprintf("%s,%s=%s", LabelSession, LabelNodeHash, NodeHashFor(f.Node))
	}

	pods, err := cs.CoreV1().Pods(ns).List(ctx, listOpts)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("list debug pods in %s: %w", ns, err))
	}

	zero := int64(0)
	var deleted []string
	for i := range pods.Items {
		p := &pods.Items[i]
		if f.TTLOnly && !shouldGC(p) {
			continue
		}
		if err := cs.CoreV1().Pods(ns).Delete(ctx, p.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &zero,
		}); err != nil && !apierrors.IsNotFound(err) {
			return deleted, exitcode.Wrap(exitcode.Cluster, fmt.Errorf("delete pod %s/%s: %w", ns, p.Name, err))
		}
		deleted = append(deleted, p.Name)
	}
	return deleted, nil
}

// SweepExpired performs the opportunistic startup GC documented in REQUIREMENTS.md FR6.
// Best-effort: errors logged via the returned error but the caller should not fail hard.
func SweepExpired(ctx context.Context, cs kubernetes.Interface, ns string) ([]string, error) {
	return Cleanup(ctx, cs, ns, CleanupFilter{TTLOnly: true})
}

func shouldGC(p *corev1.Pod) bool {
	switch p.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return true
	}
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
