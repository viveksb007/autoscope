// Smoke test: drive Phase 1 layer end-to-end on a live cluster.
// Not part of the shipped CLI; throwaway harness used during dev.
//
// Usage:
//
//	go run ./cmd/smoke <node>
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/viveksbh/autoscope/internal/debugpod"
	"github.com/viveksbh/autoscope/internal/kube"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: smoke <node>")
		os.Exit(1)
	}
	node := os.Args[1]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := kube.LoadDeps("", "")
	must(err, "LoadDeps")
	fmt.Printf("[ctx] %s\n", d.Context)

	ns := "auto-debug"
	must(debugpod.EnsureNamespace(ctx, d.Clientset, ns, "smoke-runner", false), "EnsureNamespace")
	fmt.Printf("[ns ] %s ready\n", ns)

	nc := kube.NewNodeCache(d)
	sock, err := nc.ContainerRuntimeEndpoint(ctx, node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[warn] configz: %v (using fallback %s)\n", err, sock)
	}
	fmt.Printf("[sock] %s\n", sock)

	const image = "nicolaka/netshoot:latest"

	t0 := time.Now()
	h, err := debugpod.Ensure(ctx, d.Clientset, ns, node, image, sock,
		"smoke-"+fmt.Sprint(time.Now().Unix()), "smoke-runner",
		90*time.Second)
	must(err, "Ensure")
	fmt.Printf("[pod ] %s reused=%v elapsed=%s\n", h.Name, h.Reused, time.Since(t0))
	defer h.Close()

	// Test ExecCapture: hostname via host PID 1.
	out, err := debugpod.ExecCapture(ctx, d.Clientset, d.Config, ns, h.Name,
		[]string{"nsenter", "-t", "1", "-m", "-u", "-i", "--", "/usr/bin/uname", "-a"})
	must(err, "ExecCapture uname")
	fmt.Printf("[exec] uname -a => %s", out)

	// Test ExecCapture: ctr task list (PID resolution path used by tcpdump).
	out, err = debugpod.ExecCapture(ctx, d.Clientset, d.Config, ns, h.Name,
		[]string{"nsenter", "-t", "1", "-m", "-u", "-i", "--", "/usr/bin/ctr", "-n", "k8s.io", "tasks", "list"})
	must(err, "ExecCapture ctr")
	fmt.Printf("[exec] ctr tasks (head):\n")
	for i, line := range splitLines(out) {
		if i >= 4 {
			fmt.Println("       ...")
			break
		}
		fmt.Println("       " + line)
	}

	// Test reuse: second Ensure() should be near-instant + Reused=true.
	t0 = time.Now()
	h2, err := debugpod.Ensure(ctx, d.Clientset, ns, node, image, sock,
		"smoke-reuse", "smoke-runner", 30*time.Second)
	must(err, "Ensure reuse")
	fmt.Printf("[pod ] reuse: %s reused=%v elapsed=%s\n", h2.Name, h2.Reused, time.Since(t0))
	h2.Close()

	// Cleanup: tear it all down.
	deleted, err := debugpod.Cleanup(ctx, d.Clientset, ns, debugpod.CleanupFilter{Node: node})
	must(err, "Cleanup")
	fmt.Printf("[gc  ] deleted: %v\n", deleted)
}

func must(err error, where string) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", where, err)
	os.Exit(1)
}

func splitLines(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}
