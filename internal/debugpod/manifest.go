package debugpod

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	LabelSession  = "auto.debugger/session"
	LabelNodeHash = "auto.debugger/node-hash"
	LabelTTLEpoch = "auto.debugger/ttl-epoch"
	AnnNode       = "auto.debugger/node"
	AnnCreatedBy  = "auto.debugger/created-by"
	AnnCreatedAt  = "auto.debugger/created-at"

	// DefaultTTL is the rolling TTL refreshed on each Ensure().
	DefaultTTL = 30 * time.Minute

	// HardLifeSeconds bounds the pod's max wall-clock life via `sleep`.
	// Rolling TTL annotation is metadata-only; this is the actual ceiling.
	HardLifeSeconds = 24 * 60 * 60

	containerName = "auto"
	volumeName    = "containerd-sock"
)

// PodNameFor returns the deterministic pod name for a given node.
// Hashed so long node names stay under 63-char DNS-1123 limit.
func PodNameFor(node string) string {
	sum := sha256.Sum256([]byte(node))
	return "auto-debug-" + hex.EncodeToString(sum[:])[:8]
}

// NodeHashFor returns the short hash used as label value.
func NodeHashFor(node string) string {
	sum := sha256.Sum256([]byte(node))
	return hex.EncodeToString(sum[:])[:8]
}

// BuildPodSpec constructs the privileged debug pod spec.
//
// runtimeSocket is the host filesystem path of the containerd socket,
// supplied by kube.NodeCache.ContainerRuntimeEndpoint(node).
func BuildPodSpec(node, ns, imageRef, runtimeSocket, sessionID, caller string) *corev1.Pod {
	now := time.Now()
	ttl := now.Add(DefaultTTL).Unix()

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodNameFor(node),
			Namespace: ns,
			Labels: map[string]string{
				LabelSession:  sessionID,
				LabelNodeHash: NodeHashFor(node),
				LabelTTLEpoch: strconv.FormatInt(ttl, 10),
			},
			Annotations: map[string]string{
				AnnNode:      node,
				AnnCreatedBy: caller,
				AnnCreatedAt: now.UTC().Format(time.RFC3339),
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      node,
			HostPID:       true,
			HostNetwork:   true,
			HostIPC:       true,
			RestartPolicy: corev1.RestartPolicyNever,
			Tolerations: []corev1.Toleration{
				{Key: "CriticalAddonsOnly", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "karpenter.sh/unregistered", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.kubernetes.io/not-ready", Operator: corev1.TolerationOpExists},
			},
			Containers: []corev1.Container{{
				Name:  containerName,
				Image: imageRef,
				Command: []string{
					"/bin/sh", "-c",
					fmt.Sprintf("sleep %d", HardLifeSeconds),
				},
				SecurityContext: &corev1.SecurityContext{
					Privileged: ptr.To(true),
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{"SYS_ADMIN", "NET_ADMIN", "SYS_PTRACE"},
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      volumeName,
					MountPath: runtimeSocket,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: runtimeSocket,
						Type: ptr.To(corev1.HostPathSocket),
					},
				},
			}},
		},
	}
}
