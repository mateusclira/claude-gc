package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGitHub serves a release the way GitHub does: the latest-release API,
// checksums.txt and one archive holding a stand-in claude-gc that reports
// its version like the real one.
type fakeGitHub struct {
	latest    string // tag the API reports as latest
	reports   string // what the stand-in binary prints for `version`
	corrupt   bool   // serve an archive that does not match checksums.txt
	downloads atomic.Int32
	srv       *httptest.Server
}

func newFakeGitHub(t *testing.T, latest string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{latest: latest, reports: latest}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/repos/"+Repo+"/releases/latest" {
		fmt.Fprintf(w, `{"tag_name": %q}`, f.latest)
		return
	}
	prefix := "/" + Repo + "/releases/download/"
	tag, file, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if !strings.HasPrefix(r.URL.Path, prefix) || !ok {
		http.NotFound(w, r)
		return
	}
	asset := fmt.Sprintf("claude-gc_%s_darwin_arm64.tar.gz", strings.TrimPrefix(tag, "v"))
	archive := tarball(fmt.Sprintf("#!/bin/sh\necho 'claude-gc %s (commit abc1234, 2026-11-27, go1.24 darwin/arm64)'\n", f.reports))
	sum := sha256.Sum256(archive)
	switch file {
	case "checksums.txt":
		fmt.Fprintf(w, "%s  claude-gc_%s_linux_amd64.tar.gz\n%s  %s\n",
			strings.Repeat("0", 64), strings.TrimPrefix(tag, "v"), hex.EncodeToString(sum[:]), asset)
	case asset:
		f.downloads.Add(1)
		if f.corrupt {
			archive = append(archive, 0)
		}
		_, _ = w.Write(archive)
	default:
		http.NotFound(w, r)
	}
}

// tarball builds a release archive. It is deterministic, so checksums.txt
// and the archive, served by separate requests, agree.
func tarball(script string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := [][2]string{{"LICENSE", "MIT"}, {"claude-gc", script}}
	for _, f := range files {
		must(tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o755, Size: int64(len(f[1])), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(f[1]))
		must(err)
	}
	must(tw.Close())
	must(gz.Close())
	return buf.Bytes()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// install puts an "old" binary in a temporary directory and returns options
// that upgrade it from the fake GitHub.
func (f *fakeGitHub) options(t *testing.T, current string) Options {
	t.Helper()
	target := filepath.Join(t.TempDir(), "claude-gc")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{
		Current: current, Target: target, GOOS: "darwin", GOARCH: "arm64",
		API: f.srv.URL, Downloads: f.srv.URL,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertUntouched checks that a failed or skipped upgrade left the old
// binary in place and no temporary file behind.
func assertUntouched(t *testing.T, target string) {
	t.Helper()
	if got := readFile(t, target); got != "old binary" {
		t.Errorf("binary was replaced: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Errorf("left files behind: %v", entries)
	}
}

func TestRunUpgradesToLatest(t *testing.T) {
	gh := newFakeGitHub(t, "v0.4.0")
	o := gh.options(t, "0.3.1")
	var progress []string
	o.Progress = func(s string) { progress = append(progress, s) }

	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.From != "v0.3.1" || res.To != "v0.4.0" {
		t.Errorf("result = %+v", res)
	}
	if got := readFile(t, o.Target); !strings.Contains(got, "claude-gc v0.4.0") {
		t.Errorf("binary not replaced: %q", got)
	}
	if fi, _ := os.Stat(o.Target); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(o.Target)); len(entries) != 1 {
		t.Errorf("left files behind: %v", entries)
	}
	if len(progress) != 2 || !strings.Contains(progress[1], "Checksum verified") {
		t.Errorf("progress = %q", progress)
	}
}

func TestRunUpToDate(t *testing.T) {
	for _, current := range []string{"v0.4.0", "0.4.0", "v0.4.1", "v0.4.1-0.20261127193359-cf34d59a2ce2"} {
		t.Run(current, func(t *testing.T) {
			gh := newFakeGitHub(t, "v0.4.0")
			o := gh.options(t, current)
			res, err := Run(context.Background(), o)
			if err != nil || res.Updated {
				t.Errorf("res=%+v err=%v", res, err)
			}
			if gh.downloads.Load() != 0 {
				t.Error("downloaded a release while up to date")
			}
			assertUntouched(t, o.Target)
		})
	}
}

func TestRunPinnedVersion(t *testing.T) {
	gh := newFakeGitHub(t, "v0.4.0")
	gh.reports = "v0.3.1"
	o := gh.options(t, "v0.4.0")
	o.Want = "0.3.1"
	res, err := Run(context.Background(), o)
	if err != nil || !res.Updated || res.To != "v0.3.1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, o.Target); !strings.Contains(got, "claude-gc v0.3.1") {
		t.Errorf("binary = %q", got)
	}
}

func TestRunRefusals(t *testing.T) {
	tests := []struct {
		name    string
		current string
		setup   func(*fakeGitHub, *Options)
		wantErr string
	}{
		{"checksum mismatch", "v0.3.1", func(f *fakeGitHub, _ *Options) { f.corrupt = true }, "checksum mismatch"},
		{"binary reports another version", "v0.3.1", func(f *fakeGitHub, _ *Options) { f.reports = "v0.3.9" }, "reports"},
		{"no asset for this platform", "v0.3.1", func(_ *fakeGitHub, o *Options) { o.GOARCH = "riscv64" }, "not in checksums.txt"},
		{"development build", "", nil, "development build"},
		{"git describe build", "v0.3.0-6-gd772047-dirty", nil, ""},
		{"homebrew copy", "v0.3.1", func(_ *fakeGitHub, o *Options) {
			dir := filepath.Join(filepath.Dir(o.Target), "Cellar", "claude-gc", "0.3.1", "bin")
			must(os.MkdirAll(dir, 0o755))
			o.Target = filepath.Join(dir, "claude-gc")
			must(os.WriteFile(o.Target, []byte("old binary"), 0o755))
		}, "brew upgrade"},
		{"not a release tag", "v0.3.1", func(_ *fakeGitHub, o *Options) { o.Want = "main" }, "not a release version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := newFakeGitHub(t, "v0.4.0")
			o := gh.options(t, tt.current)
			if tt.setup != nil {
				tt.setup(gh, &o)
			}
			res, err := Run(context.Background(), o)
			if tt.wantErr == "" {
				// A build that is not a clean release but parses as an
				// older one is upgraded like any other.
				if err != nil || !res.Updated {
					t.Errorf("res=%+v err=%v", res, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
			if res.Updated {
				t.Error("reported Updated after a failure")
			}
			assertUntouched(t, o.Target)
		})
	}
}

func TestCheckDoesNotDownload(t *testing.T) {
	gh := newFakeGitHub(t, "v0.4.0")
	o := gh.options(t, "v0.3.1")
	res, err := Check(context.Background(), o)
	if err != nil || !res.Updated || res.To != "v0.4.0" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gh.downloads.Load() != 0 {
		t.Error("Check downloaded the release")
	}
	assertUntouched(t, o.Target)

	o.Current = ""
	if _, err := Check(context.Background(), o); !errors.Is(err, ErrDevBuild) {
		t.Errorf("dev build: err = %v", err)
	}
}

func TestLatestFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := Check(context.Background(), Options{Current: "v0.3.1", API: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v0.3.1", "v0.4.0", -1},
		{"v0.10.0", "v0.9.9", 1},
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0+meta", "v1.0.0", 0},
		{"v0.3.2-0.20261127193359-cf34d59a2ce2", "v0.3.1", 1},
	}
	for _, tt := range tests {
		a, okA := parseVersion(tt.a)
		b, okB := parseVersion(tt.b)
		if !okA || !okB {
			t.Fatalf("parse %q/%q failed", tt.a, tt.b)
		}
		if got := compare(a, b); got != tt.want {
			t.Errorf("compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
	for _, bad := range []string{"", "dev", "0.3.1", "v0.3", "v0.x.1", "main"} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("parseVersion(%q) succeeded", bad)
		}
	}
}
