package nsenter

import "testing"

func TestStripContainerIDPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"containerd://abc123", "abc123"},
		{"docker://deadbeef", "deadbeef"},
		{"abc123", "abc123"},
		{"", ""},
	}
	for _, c := range cases {
		if got := StripContainerIDPrefix(c.in); got != c.want {
			t.Errorf("StripContainerIDPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTaskList(t *testing.T) {
	out := []byte(`TASK                                                                PID      STATUS
a877da9821603533f8b1a660d33a89e0b61c02fed11808a3889320177281ff86    2113     RUNNING
8b353b02d342067cb46d77dd4f5fb0d83262c2f748e67a3ebc7bc0548c24c88c    2202     RUNNING

`)
	tasks := ParseTaskList(out)
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if pid := tasks["a877da9821603533f8b1a660d33a89e0b61c02fed11808a3889320177281ff86"]; pid != 2113 {
		t.Errorf("first PID = %d, want 2113", pid)
	}
	if pid := tasks["8b353b02d342067cb46d77dd4f5fb0d83262c2f748e67a3ebc7bc0548c24c88c"]; pid != 2202 {
		t.Errorf("second PID = %d, want 2202", pid)
	}
}

func TestFindPID(t *testing.T) {
	out := []byte("TASK PID STATUS\nabc 42 RUNNING\nxyz 99 RUNNING\n")
	pid, err := FindPID(out, "abc")
	if err != nil || pid != 42 {
		t.Fatalf("got pid=%d err=%v, want 42 nil", pid, err)
	}
	if _, err := FindPID(out, "missing"); err == nil {
		t.Fatal("missing container should error")
	}
}

func TestArgvBuilders(t *testing.T) {
	if got := HostMount("ls"); got[0] != "nsenter" || got[len(got)-1] != "ls" {
		t.Errorf("HostMount produced %v", got)
	}
	// HostNet: ["nsenter","-t","1","-n","--", ...args]
	if got := HostNet("curl", "-sS", "url"); got[4] != "--" {
		t.Errorf("HostNet missing -- separator at index 4: %v", got)
	}
	if got := PodNet(2202, "tcpdump"); got[2] != "2202" {
		t.Errorf("PodNet pid not in argv: %v", got)
	}
}

func TestTcpdumpRawTimeoutBranch(t *testing.T) {
	with := TcpdumpRaw(2202, "any", 65535, "port 80", 10)
	without := TcpdumpRaw(2202, "any", 65535, "port 80", 0)

	hasTimeout := false
	for _, a := range with {
		if a == "/usr/bin/timeout" {
			hasTimeout = true
		}
	}
	if !hasTimeout {
		t.Errorf("dur > 0 should include timeout: %v", with)
	}
	for _, a := range without {
		if a == "/usr/bin/timeout" {
			t.Errorf("dur == 0 must omit timeout: %v", without)
		}
	}
}
