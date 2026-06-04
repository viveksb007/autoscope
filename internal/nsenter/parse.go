package nsenter

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseTaskList extracts (containerID, pid) pairs from the output of
// `ctr -n k8s.io tasks list`. Header line is skipped; lines that don't
// have at least 2 whitespace-separated fields are dropped.
//
// Sample line:
//
//	8b353b02d342067cb46d77...    2202   RUNNING
func ParseTaskList(out []byte) map[string]int {
	pids := make(map[string]int)
	for i, line := range strings.Split(string(out), "\n") {
		// Skip header.
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		pids[fields[0]] = pid
	}
	return pids
}

// FindPID returns the PID for the given containerID (full SHA, no prefix).
// Returns an error if not found.
func FindPID(out []byte, containerID string) (int, error) {
	tasks := ParseTaskList(out)
	if pid, ok := tasks[containerID]; ok {
		return pid, nil
	}
	return 0, fmt.Errorf("container %s not found in ctr task list", containerID)
}

// StripContainerIDPrefix removes the runtime URI prefix from a Kubernetes
// containerStatus.containerID value, e.g. "containerd://abcdef..." -> "abcdef...".
func StripContainerIDPrefix(s string) string {
	if _, after, ok := strings.Cut(s, "://"); ok {
		return after
	}
	return s
}
