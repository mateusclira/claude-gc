package proc

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const macOSSample = `    1     0  21504 Sun Sep  6 12:50:01 2026 /sbin/launchd
 1797  1353 364992 Sun Sep  6 12:52:27 2026 /Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app/Contents/MacOS/Code Helper (Plugin) --type=utility --user-data-dir=/Users/me/Library/Application Support/Code
 1873  1797 288064 Mon Oct 26 17:43:31 2026 /Users/me/.vscode/extensions/anthropic.claude-code-2.1.259-darwin-arm64/resources/native-binary/claude --output-format stream-json --verbose
95113 94394 296816 Fri Nov 27 13:43:33 2026 /Users/me/.vscode/extensions/anthropic.claude-code-2.1.332-darwin-arm64/resources/native-binary/claude
 3871 95113   2048 Fri Nov 27 13:44:01 2026 /bin/zsh
garbage line that must be skipped
`

const linuxSample = `      1       0  11264 Thu Nov 27 08:00:00 2026 /sbin/init splash
  41162    1797 217536 Mon Oct  6 10:19:24 2026 /home/me/.vscode/extensions/anthropic.claude-code-2.1.259-linux-x64/resources/native-binary/claude --verbose
  50001    1797  90000 Tue Nov 25 09:00:00 2026 /usr/bin/node /home/me/.npm-global/lib/node_modules/@anthropic-ai/claude-code/cli.js
`

func TestParseMacOS(t *testing.T) {
	table, err := Parse([]byte(macOSSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 5 {
		t.Fatalf("got %d processes, want 5", len(table))
	}
	p := table[1873]
	if p.PPID != 1797 || p.RSSKB != 288064 {
		t.Errorf("unexpected process %+v", p)
	}
	want := time.Date(2026, 10, 26, 17, 43, 31, 0, time.UTC)
	if !p.Start.Equal(want) {
		t.Errorf("start = %v, want %v", p.Start, want)
	}
	if !strings.HasSuffix(p.Args, "--output-format stream-json --verbose") {
		t.Errorf("args = %q", p.Args)
	}
	if p.Name() != "claude" {
		t.Errorf("name = %q", p.Name())
	}
	// Single-digit day padded with two spaces must still parse.
	if got := table[1797].Start; !got.Equal(time.Date(2026, 9, 6, 12, 52, 27, 0, time.UTC)) {
		t.Errorf("padded day start = %v", got)
	}
	if name := table[1797].Name(); name != "Code" {
		// The path contains spaces; Name() only promises a best effort.
		t.Logf("name of extension host = %q", name)
	}
}

func TestParseLinux(t *testing.T) {
	table, err := Parse([]byte(linuxSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 3 {
		t.Fatalf("got %d processes, want 3", len(table))
	}
	if got := table[41162].Start; !got.Equal(time.Date(2026, 10, 6, 10, 19, 24, 0, time.UTC)) {
		t.Errorf("start = %v", got)
	}
	if !IsClaude(table[41162].Args) {
		t.Errorf("native binary not recognised: %q", table[41162].Args)
	}
	if !IsClaude(table[50001].Args) {
		t.Errorf("npm distribution not recognised: %q", table[50001].Args)
	}
	if IsClaude(table[1].Args) {
		t.Errorf("init recognised as claude")
	}
}

func TestIsClaude(t *testing.T) {
	yes := []string{
		"claude",
		"claude --resume abc",
		"/opt/homebrew/bin/claude",
		"/Users/me/.local/share/claude/versions/2.1.215 --verbose",
		"/usr/bin/node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js",
	}
	no := []string{
		"/Applications/Visual Studio Code.app/Contents/MacOS/Code",
		"/Users/me/.vscode/extensions/anthropic.claude-code-2.1.259-darwin-arm64/dist/extension.js",
		"node claude-gc daemon",
		"/usr/local/bin/claude-gc daemon",
		"python3 claudette.py",
	}
	for _, a := range yes {
		if !IsClaude(a) {
			t.Errorf("IsClaude(%q) = false, want true", a)
		}
	}
	for _, a := range no {
		if IsClaude(a) {
			t.Errorf("IsClaude(%q) = true, want false", a)
		}
	}
}

func TestIsClaudeVersionedPath(t *testing.T) {
	// The native installer's binary lives in a versioned directory and is
	// executed through a symlink; ps may show either path.
	if !IsClaude("/Users/me/.local/bin/claude") {
		t.Error("symlink path not recognised")
	}
}

func TestAncestors(t *testing.T) {
	table, _ := Parse([]byte(macOSSample))
	chain := table.Ancestors(3871)
	for _, pid := range []int{3871, 95113, 94394} {
		if !chain[pid] {
			t.Errorf("pid %d missing from ancestor chain %v", pid, chain)
		}
	}
	if chain[1873] {
		t.Error("unrelated pid 1873 in ancestor chain")
	}
	// Cycles must not hang.
	table[7] = Process{PID: 7, PPID: 8}
	table[8] = Process{PID: 8, PPID: 7}
	if got := len(table.Ancestors(7)); got != 2 {
		t.Errorf("cycle: got %d ancestors", got)
	}
}

func TestParseStart(t *testing.T) {
	got, err := ParseStart("Fri Nov 27 13:43:33 2026")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 11, 27, 13, 43, 33, 0, time.UTC)) {
		t.Errorf("got %v", got)
	}
	if _, err := ParseStart("yesterday"); err == nil {
		t.Error("expected error")
	}
}

func TestSplitFields(t *testing.T) {
	fields, rest := splitFields("  1  2 3   a b  c ", 3)
	if len(fields) != 3 || fields[2] != "3" {
		t.Errorf("fields = %v", fields)
	}
	if rest != "a b  c " {
		t.Errorf("rest = %q", rest)
	}
	fields, rest = splitFields(" 1 2", 3)
	if len(fields) != 2 || rest != "" {
		t.Errorf("short line: fields=%v rest=%q", fields, rest)
	}
}

// TestSnapshotLive exercises the real ps(1) on the host.
func TestSnapshotLive(t *testing.T) {
	table, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	self, ok := table[os.Getpid()]
	if !ok {
		t.Fatalf("own pid %d not in snapshot of %d processes", os.Getpid(), len(table))
	}
	if self.Start.IsZero() {
		t.Error("own start time did not parse")
	}
	if time.Since(self.Start) > time.Hour {
		t.Errorf("own start time %v is implausible", self.Start)
	}
	start, found, err := StartTime(os.Getpid())
	if err != nil || !found {
		t.Fatalf("StartTime: found=%v err=%v", found, err)
	}
	if !start.Equal(self.Start) {
		t.Errorf("StartTime %v != snapshot %v", start, self.Start)
	}
	if _, found, _ := StartTime(2147483000); found {
		t.Error("StartTime found an implausible pid")
	}
}

// TestTerminateLive kills a real child process.
func TestTerminateLive(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if !Alive(pid) {
		t.Fatal("child not alive")
	}
	forced, err := Terminate(pid, 2*time.Second)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if forced {
		t.Error("sleep should exit on SIGTERM without SIGKILL")
	}
	select {
	case err := <-done:
		t.Logf("child exited: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	if Alive(pid) {
		t.Error("child still alive")
	}
	if _, err := Terminate(0, time.Second); err == nil {
		t.Error("expected refusal for pid 0")
	}
}
