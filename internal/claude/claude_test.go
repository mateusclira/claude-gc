package claude

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere/claude")
	if got := ConfigDir(); got != "/elsewhere/claude" {
		t.Errorf("ConfigDir = %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := ConfigDir(); got != filepath.Join(home, ".claude") {
		t.Errorf("ConfigDir = %q", got)
	}
}

func TestReadRecords(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("39953.json", `{"sessionId":"f7c1ed71-1eb8-440d-b324-d1d341693cb7","cwd":"/home/me/app","startedAt":1790163912306,"procStart":"Wed Sep 23 11:45:12 2026","version":"2.1.259","kind":"interactive","entrypoint":"claude-vscode","messagingSocketPath":"/tmp/cc-socks/39953.sock","name":"app-0d"}`)
	write("95113.json", `{"pid":95113,"sessionId":"d01fcc06","cwd":"/home/me/gc","startedAt":1795787013570,"procStart":"Fri Nov 27 13:43:33 2026","entrypoint":"claude-vscode","name":"gc-99","status":"busy","updatedAt":1795787297604}`)
	write("95113.66b23c13.key", `{"peerToken":"x"}`)
	write("broken.json", `{}`)
	write("12.json", `{not json`)
	write("notes.txt", `hello`)

	records, problems := ReadRecords(dir)
	if len(problems) != 1 {
		t.Errorf("problems = %v, want exactly one (malformed 12.json)", problems)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	r := records[0]
	if r.PID != 39953 || r.Name != "app-0d" || r.Entrypoint != "claude-vscode" || r.SocketPath != "/tmp/cc-socks/39953.sock" {
		t.Errorf("unexpected record %+v", r)
	}
	if r.Path != filepath.Join(dir, "39953.json") {
		t.Errorf("path = %q", r.Path)
	}
	if got := r.Started(); !got.Equal(time.UnixMilli(1790163912306)) {
		t.Errorf("Started = %v", got)
	}
	ps, ok := r.ProcStartTime()
	if !ok || !ps.Equal(time.Date(2026, 9, 23, 11, 45, 12, 0, time.UTC)) {
		t.Errorf("ProcStartTime = %v %v", ps, ok)
	}
	if records[1].PID != 95113 || records[1].Status != "busy" {
		t.Errorf("unexpected second record %+v", records[1])
	}
	if got := (Record{SessionID: "d01fcc06-24cc"}).DisplayName(); got != "d01fcc06" {
		t.Errorf("DisplayName fallback = %q", got)
	}

	files := RelatedFiles(dir, 95113)
	if len(files) != 2 {
		t.Fatalf("RelatedFiles = %v", files)
	}
	if files[0] != filepath.Join(dir, "95113.66b23c13.key") || files[1] != filepath.Join(dir, "95113.json") {
		t.Errorf("RelatedFiles = %v", files)
	}
	if got := RelatedFiles(dir, 9511); len(got) != 0 {
		t.Errorf("prefix 9511 must not match 95113: %v", got)
	}
}

func TestReadRecordsMissingDir(t *testing.T) {
	records, problems := ReadRecords(filepath.Join(t.TempDir(), "nope"))
	if records != nil || problems != nil {
		t.Errorf("missing dir: records=%v problems=%v", records, problems)
	}
}

func TestTranscripts(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	old := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, f := range []struct {
		dir, name string
		mtime     time.Time
	}{
		{"-home-me-app", "aaaa.jsonl", old},
		{"-home-me-app", "bbbb.jsonl", old.Add(48 * time.Hour)},
		{"-home-me-other", "aaaa.jsonl", old.Add(time.Hour)}, // same id elsewhere: newest wins
		{"-home-me-other", "memory", time.Time{}},            // a directory, ignored
	} {
		dir := filepath.Join(projects, f.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, f.name)
		if f.mtime.IsZero() {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	index, err := Transcripts(projects)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Fatalf("index = %v", index)
	}
	if !index["aaaa"].Equal(old.Add(time.Hour)) {
		t.Errorf("aaaa = %v, want newest copy", index["aaaa"])
	}
	if !index["bbbb"].Equal(old.Add(48 * time.Hour)) {
		t.Errorf("bbbb = %v", index["bbbb"])
	}
	empty, err := Transcripts(filepath.Join(root, "missing"))
	if err != nil || len(empty) != 0 {
		t.Errorf("missing projects dir: %v %v", empty, err)
	}
}

// shortTempDir returns a temp dir with a short path: Unix socket paths are
// limited to about 100 bytes and t.TempDir() is often longer than that.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "claude-gc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSocketDirsAndHelpers(t *testing.T) {
	tmp := shortTempDir(t)
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "cc-socks")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range SocketDirs() {
		if d == dir {
			found = true
		}
	}
	if !found {
		t.Errorf("SocketDirs = %v, want it to include %s", SocketDirs(), dir)
	}

	sock := filepath.Join(dir, "4242.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if !IsSocket(sock) {
		t.Error("IsSocket = false for a listening socket")
	}
	if IsSocket(filepath.Join(dir, "missing.sock")) {
		t.Error("IsSocket = true for a missing path")
	}
	if pid, ok := SocketPID("4242.sock"); !ok || pid != 4242 {
		t.Errorf("SocketPID = %d %v", pid, ok)
	}
	for _, bad := range []string{"4242", "x.sock", "-1.sock", "0.sock"} {
		if _, ok := SocketPID(bad); ok {
			t.Errorf("SocketPID(%q) accepted", bad)
		}
	}
}
