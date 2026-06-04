package debugpod

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/viveksbh/autoscope/internal/exitcode"
)

const (
	PSAEnforceLabel = "pod-security.kubernetes.io/enforce"
	PSAPrivileged   = "privileged"
)

// EnsureNamespace implements the state machine from docs/DESIGN.md:
//   - absent      → Create with PSA-privileged label.
//   - present+OK  → no-op.
//   - present−lbl + autoLabel → Patch label.
//   - present−lbl + !autoLabel → user-error refusal.
func EnsureNamespace(ctx context.Context, cs kubernetes.Interface, ns, caller string, autoLabel bool) error {
	got, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("get namespace %s: %w", ns, err))
	}

	if apierrors.IsNotFound(err) {
		obj := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   ns,
				Labels: map[string]string{PSAEnforceLabel: PSAPrivileged},
				Annotations: map[string]string{
					AnnCreatedBy: caller,
				},
			},
		}
		if _, err := cs.CoreV1().Namespaces().Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("create namespace %s: %w", ns, err))
		}
		return nil
	}

	if got.Labels[PSAEnforceLabel] == PSAPrivileged {
		return nil
	}

	if !autoLabel {
		return exitcode.Wrap(exitcode.User, fmt.Errorf(
			"namespace %s exists but lacks '%s=%s'. Re-run with --auto-label to patch, or pre-create namespace with the label",
			ns, PSAEnforceLabel, PSAPrivileged))
	}

	patch := []byte(fmt.Sprintf(
		`{"metadata":{"labels":{"%s":"%s"}}}`,
		PSAEnforceLabel, PSAPrivileged))
	if _, err := cs.CoreV1().Namespaces().Patch(
		ctx, ns, types.StrategicMergePatchType, patch, metav1.PatchOptions{},
	); err != nil {
		return exitcode.Wrap(exitcode.Cluster, fmt.Errorf("patch namespace %s: %w", ns, err))
	}
	return nil
}
