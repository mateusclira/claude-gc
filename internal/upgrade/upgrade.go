// Package upgrade replaces the claude-gc binary with a release from GitHub.
//
// It does what scripts/install.sh does: download the release archive for
// this platform, check it against the release's checksums.txt, and install
// the binary. The new binary is written next to the old one and has to run
// and report the expected version before it is renamed over it, so a failed
// upgrade leaves the old binary untouched.
package upgrade

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases come from.
const Repo = "mateusclira/claude-gc"

// Size limits for what is downloaded and unpacked.
const (
	maxChecksums = 64 << 10
	maxArchive   = 64 << 20
)

// Options says what to upgrade and where releases come from.
type Options struct {
	Current string // version running now, such as "v0.3.1"; empty for a development build
	Want    string // release tag to install; empty for the latest release
	Target  string // path of the binary to replace
	GOOS    string
	GOARCH  string

	// Endpoints, overridable in tests.
	API       string // default https://api.github.com
	Downloads string // default https://github.com
	Client    *http.Client
	UserAgent string

	// Progress, when set, receives one line per step.
	Progress func(string)
}

// Result reports what happened.
type Result struct {
	From    string // the version that was running
	To      string // the release that was chosen
	Updated bool   // false when nothing was replaced
}

// ErrDevBuild means the running binary has no release version to compare
// with, so upgrading needs an explicit version.
var ErrDevBuild = errors.New("this is a development build, so there is no release to compare it with; pass -version with a release tag, such as -version v0.3.1, to replace it")

// Check resolves the release Run would install, without installing it.
// Updated is true when that release differs from the running version.
func Check(ctx context.Context, o Options) (Result, error) {
	o = o.withDefaults()
	res := Result{From: canonical(o.Current)}
	tag := canonical(o.Want)
	if tag == "" {
		latest, err := o.latest(ctx)
		if err != nil {
			return res, err
		}
		tag = latest
	}
	if _, ok := parseVersion(tag); !ok {
		return res, fmt.Errorf("%q is not a release version", o.Want)
	}
	res.To = tag
	if o.Want == "" {
		cur, ok := parseVersion(res.From)
		if !ok {
			return res, ErrDevBuild
		}
		latest, _ := parseVersion(tag)
		// Only move forward on our own, so a build newer than the latest
		// release is left alone.
		res.Updated = compare(cur, latest) < 0
		return res, nil
	}
	res.Updated = res.From != tag
	return res, nil
}

// Run installs the release chosen as in Check over o.Target.
func Run(ctx context.Context, o Options) (Result, error) {
	o = o.withDefaults()
	res, err := Check(ctx, o)
	if err != nil || !res.Updated {
		return res, err
	}
	if strings.Contains(o.Target, "/Cellar/") || strings.Contains(o.Target, "/Caskroom/") {
		res.Updated = false
		return res, errors.New("this copy is managed by Homebrew; upgrade it with `brew upgrade claude-gc`")
	}
	bin, err := o.download(ctx, res.To)
	if err != nil {
		res.Updated = false
		return res, err
	}
	if err := o.replace(ctx, bin, res.To); err != nil {
		res.Updated = false
		return res, err
	}
	return res, nil
}

func (o Options) withDefaults() Options {
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Downloads == "" {
		o.Downloads = "https://github.com"
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	if o.UserAgent == "" {
		o.UserAgent = "claude-gc"
	}
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
	return o
}

// latest asks GitHub for the tag of the newest release.
func (o Options) latest(ctx context.Context) (string, error) {
	body, err := o.get(ctx, o.API+"/repos/"+Repo+"/releases/latest", maxChecksums)
	if err != nil {
		return "", fmt.Errorf("finding the latest release: %w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		return "", errors.New("finding the latest release: unexpected response from GitHub")
	}
	return canonical(rel.TagName), nil
}

// download fetches the release archive for this platform, verifies it
// against checksums.txt and returns the claude-gc binary inside it.
func (o Options) download(ctx context.Context, tag string) ([]byte, error) {
	asset := fmt.Sprintf("claude-gc_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), o.GOOS, o.GOARCH)
	base := o.Downloads + "/" + Repo + "/releases/download/" + tag + "/"

	o.Progress(fmt.Sprintf("Downloading claude-gc %s for %s/%s", tag, o.GOOS, o.GOARCH))
	sums, err := o.get(ctx, base+"checksums.txt", maxChecksums)
	if err != nil {
		return nil, fmt.Errorf("downloading checksums.txt: %w", err)
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return nil, err
	}
	archive, err := o.get(ctx, base+asset, maxArchive)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", asset, err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s: the download is not the published release", asset)
	}
	o.Progress("Checksum verified against the release's checksums.txt")
	return extract(archive, "claude-gc")
}

// replace installs bin over o.Target once it has proved that it runs.
func (o Options) replace(ctx context.Context, bin []byte, tag string) error {
	dir := filepath.Dir(o.Target)
	tmp, err := os.CreateTemp(dir, ".claude-gc-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, tmp.Name(), "version").Output()
	if err != nil {
		return fmt.Errorf("the downloaded binary does not run on this machine: %w", err)
	}
	if fields := strings.Fields(string(out)); len(fields) < 2 || canonical(fields[1]) != tag {
		return fmt.Errorf("the downloaded binary reports %q, not %s", strings.TrimSpace(string(out)), tag)
	}
	if err := os.Rename(tmp.Name(), o.Target); err != nil {
		return fmt.Errorf("replacing %s: %w", o.Target, err)
	}
	return nil
}

func (o Options) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", o.UserAgent)
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s: response larger than %d bytes", url, limit)
	}
	return body, nil
}

// checksumFor finds the SHA-256 of asset in a checksums.txt file.
func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == asset && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no release asset for this platform: %s is not in checksums.txt", asset)
}

// extract returns the regular file called name at the root of a .tar.gz.
func extract(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("reading the release archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the release archive has no %s binary", name)
		}
		if err != nil {
			return nil, fmt.Errorf("reading the release archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Clean(h.Name) == name {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

// canonical writes a version the way tags are written: "0.3.1" and "v0.3.1"
// both become "v0.3.1". Anything else is returned unchanged.
func canonical(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

type version struct {
	nums [3]int
	pre  string
}

// parseVersion reads a semantic version such as v1.2.3 or v1.2.3-rc.1.
func parseVersion(v string) (version, bool) {
	var out version
	s, ok := strings.CutPrefix(v, "v")
	if !ok {
		return out, false
	}
	s, _, _ = strings.Cut(s, "+")
	s, out.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out.nums[i] = n
	}
	return out, true
}

// compare orders versions; a pre-release sorts before its release.
func compare(a, b version) int {
	for i := range a.nums {
		if a.nums[i] != b.nums[i] {
			if a.nums[i] < b.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	case a.pre < b.pre:
		return -1
	default:
		return 1
	}
}
