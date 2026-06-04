// Package nsenter constructs `nsenter` argv slices for the three execution
// modes used by autoscope. Pure: no kube or io deps. See docs/DESIGN.md
// "Exec Streaming" and docs/VERIFY.md for the verified namespace flag matrix.
package nsenter

import "strconv"

// HostMount enters host PID 1's mount/uts/ipc namespaces. Use for host
// binaries (journalctl, systemctl-where-allowed, /usr/bin/ctr).
//
//	nsenter -t 1 -m -u -i -- <cmd...>
func HostMount(cmd ...string) []string {
	return append([]string{"nsenter", "-t", "1", "-m", "-u", "-i", "--"}, cmd...)
}

// HostNet enters host PID 1's net namespace only — keeps the pod's mount
// namespace so netshoot binaries (curl, tcpdump) are reachable. Used for
// reaching host loopback services.
//
//	nsenter -t 1 -n -- <cmd...>
func HostNet(cmd ...string) []string {
	return append([]string{"nsenter", "-t", "1", "-n", "--"}, cmd...)
}

// PodNet enters a workload PID's net namespace only. Used to capture
// traffic from a target pod via tcpdump in netshoot's mount namespace.
//
//	nsenter -t <pid> -n -- <cmd...>
func PodNet(pid int, cmd ...string) []string {
	return append([]string{"nsenter", "-t", strconv.Itoa(pid), "-n", "--"}, cmd...)
}

// HostExec is the generic builder for `auto exec`. It accepts an explicit
// namespace flag set so the user can enter mount+pid+net+ipc+uts+cgroup
// in any combination.
func HostExec(nsFlags []string, cmd ...string) []string {
	out := []string{"nsenter", "-t", "1"}
	out = append(out, nsFlags...)
	out = append(out, "--")
	return append(out, cmd...)
}

// CtrTaskList runs `ctr -n k8s.io tasks list` inside the host mount ns.
// Output is parsed by ParseTaskList for PID resolution.
func CtrTaskList() []string {
	return HostMount("/usr/bin/ctr", "-n", "k8s.io", "tasks", "list")
}

// JournalCtl builds journalctl argv for `auto logs`. Caller assembles
// flag values (-u, --since, -n, -f, --no-pager, etc.) into `flags`.
func JournalCtl(unit string, flags []string) []string {
	args := []string{"/usr/bin/journalctl", "-u", unit, "--no-pager"}
	args = append(args, flags...)
	return HostMount(args...)
}

// JournalUnitProbe builds the round-2-verified unit-existence probe:
// stdout-byte-count > 0 means unit known; == 0 means unit absent.
func JournalUnitProbe(unit string) []string {
	return HostMount("/usr/bin/journalctl", "-u", unit, "-n", "1",
		"--output=cat", "--no-pager")
}

// TailFile builds a `tail` invocation in the host mount-ns for a file log.
//
// follow=true ⇒ -F (re-open on rotation, equivalent to journalctl -f).
// lines       ⇒ -n N if > 0, otherwise tail's default last 10.
func TailFile(path string, follow bool, lines int) []string {
	args := []string{"/usr/bin/tail"}
	if follow {
		args = append(args, "-F")
	}
	if lines > 0 {
		args = append(args, "-n", strconv.Itoa(lines))
	}
	args = append(args, path)
	return HostMount(args...)
}

// FileExistsProbe returns argv that succeeds (exit 0) iff `path` is a regular
// file on the host. Used by `auto logs` source-existence preflight.
func FileExistsProbe(path string) []string {
	return HostMount("/usr/bin/test", "-f", path)
}

// FindSystemdUnitFile builds a fallback discovery for unit files when
// journalctl probe is empty. Searches both /etc/systemd/system and
// the arch-suffixed sys-root path.
func FindSystemdUnitFile(unit, arch string) []string {
	sysrootPath := "/" + arch + "-bottlerocket-linux-gnu/sys-root/usr/lib/systemd/system"
	return HostMount("/usr/bin/find",
		"/etc/systemd/system", sysrootPath,
		"-maxdepth", "3", "-name", unit+".service")
}

// CurlLocalhost builds a curl invocation that reaches host loopback
// services from netshoot's mount namespace.
func CurlLocalhost(scheme string, port int, path string, timeoutSecs int) []string {
	url := scheme + "://127.0.0.1:" + strconv.Itoa(port) + path
	return HostNet("/usr/bin/curl", "-sS", "-m", strconv.Itoa(timeoutSecs), url)
}

// TcpdumpRaw builds a tcpdump invocation in the workload pod's net ns,
// writing raw pcap to stdout. Caller sets snaplen/iface/filter via flags.
//
// The remote process is wrapped with busybox `timeout -s INT -k 2 <dur>` by
// the caller when a duration cap is wanted; pass 0 to omit the wrapper.
func TcpdumpRaw(wpid int, iface string, snaplen int, filter string, durationSecs int) []string {
	td := []string{
		"/usr/bin/tcpdump",
		"-i", iface,
		"-U",
		"-s", strconv.Itoa(snaplen),
		"-w", "-",
	}
	if filter != "" {
		td = append(td, filter)
	}
	if durationSecs > 0 {
		// busybox timeout: -s SIG -k KILL_SECS DURATION_SECS PROG ARGS
		head := []string{"/usr/bin/timeout", "-s", "INT", "-k", "2", strconv.Itoa(durationSecs)}
		td = append(head, td...)
	}
	return PodNet(wpid, td...)
}

// TcpdumpSummary builds a tcpdump invocation that prints human-readable
// packet lines (no -w). Used for `--summary` mode.
func TcpdumpSummary(wpid int, iface string, count int, filter string, durationSecs int) []string {
	td := []string{
		"/usr/bin/tcpdump",
		"-i", iface,
		"-nn", "-l",
	}
	if count > 0 {
		td = append(td, "-c", strconv.Itoa(count))
	}
	if filter != "" {
		td = append(td, filter)
	}
	if durationSecs > 0 {
		head := []string{"/usr/bin/timeout", "-s", "INT", "-k", "2", strconv.Itoa(durationSecs)}
		td = append(head, td...)
	}
	return PodNet(wpid, td...)
}
