// Package upgrade implements the deliberately small, Linux-only self-update
// path for the ticket executable.
package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repository        = "toolsupply/ticket"
	defaultAPI        = "https://api.github.com"
	defaultDownload   = "https://github.com/toolsupply/ticket/releases/download"
	maxReleaseJSON    = 1 << 20
	maxChecksums      = 1 << 20
	maxArchive        = 128 << 20
	maxExecutable     = 128 << 20
	validationTimeout = 3 * time.Second
)

var ErrUnsupportedPlatform = errors.New("unsupported upgrade platform")

// Options contains process facts and testable network endpoints. The CLI
// supplies only CurrentVersion in normal use; the other fields are defaults
// for the running process and are not user configuration.
type Options struct {
	CurrentVersion  string
	GOOS            string
	GOARCH          string
	ExecutablePath  string
	APIBaseURL      string
	DownloadBaseURL string
	HTTPClient      *http.Client
}

// Result describes the update decision and, when applicable, the installed
// release. Versions are normalized without a leading v.
type Result struct {
	Changed    bool   `json:"changed"`
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	Executable string `json:"executable,omitempty"`
}

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type releaseVersion struct {
	major int
	minor int
	patch int
}

// Run resolves and, if needed, installs the latest stable release.
func Run(opts Options) (Result, error) {
	if opts.GOOS == "" {
		opts.GOOS = runtimeGOOS()
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtimeGOARCH()
	}
	if opts.GOOS != "linux" {
		return Result{}, fmt.Errorf("%w: ticket upgrade is supported on Linux only (running on %s)", ErrUnsupportedPlatform, opts.GOOS)
	}
	archiveArch, ok := archiveArchitecture(opts.GOARCH)
	if !ok {
		return Result{}, fmt.Errorf("%w: ticket upgrade does not support Linux architecture %s; use amd64 or arm64", ErrUnsupportedPlatform, opts.GOARCH)
	}
	current, err := parseVersion(opts.CurrentVersion)
	if err != nil {
		return Result{}, fmt.Errorf("current ticket version %q is not a usable release version", opts.CurrentVersion)
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	apiBase := strings.TrimRight(opts.APIBaseURL, "/")
	if apiBase == "" {
		apiBase = defaultAPI
	}
	downloadBase := strings.TrimRight(opts.DownloadBaseURL, "/")
	if downloadBase == "" {
		downloadBase = defaultDownload
	}

	releaseInfo, err := fetchRelease(client, apiBase)
	if err != nil {
		return Result{}, err
	}
	latest, err := parseVersion(releaseInfo.TagName)
	if err != nil || releaseInfo.Draft || releaseInfo.Prerelease {
		return Result{}, fmt.Errorf("GitHub returned an unusable stable release %q", releaseInfo.TagName)
	}
	if compareVersion(latest, current) <= 0 {
		return Result{Changed: false, OldVersion: formatVersion(current), NewVersion: formatVersion(latest)}, nil
	}

	archiveName := "ticket-linux-" + archiveArch + ".tar.gz"
	checksumsURL := downloadBase + "/" + url.PathEscape(releaseInfo.TagName) + "/SHA256SUMS"
	archiveURL := downloadBase + "/" + url.PathEscape(releaseInfo.TagName) + "/" + archiveName
	checksums, err := fetchBytes(client, checksumsURL, maxChecksums)
	if err != nil {
		return Result{}, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, err := checksumFor(checksums, archiveName)
	if err != nil {
		return Result{}, err
	}
	archiveBytes, err := fetchBytes(client, archiveURL, maxArchive)
	if err != nil {
		return Result{}, fmt.Errorf("download %s: %w", archiveName, err)
	}
	got := sha256.Sum256(archiveBytes)
	if !strings.EqualFold(want, hex.EncodeToString(got[:])) {
		return Result{}, fmt.Errorf("checksum mismatch for %s", archiveName)
	}

	tmpDir, err := os.MkdirTemp("", "ticket-upgrade-")
	if err != nil {
		return Result{}, fmt.Errorf("create upgrade temporary directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, archiveName)
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		return Result{}, fmt.Errorf("write upgrade archive: %w", err)
	}
	extracted, err := extractExecutable(archivePath, tmpDir)
	if err != nil {
		return Result{}, err
	}
	if err := validateExecutable(extracted, archiveArch); err != nil {
		return Result{}, err
	}

	target := opts.ExecutablePath
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("find running ticket executable: %w", err)
		}
	}
	if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
		target = resolved
	}
	if err := replaceExecutable(target, extracted); err != nil {
		return Result{}, fmt.Errorf("cannot replace %s: %w", target, err)
	}
	return Result{Changed: true, OldVersion: formatVersion(current), NewVersion: formatVersion(latest), Executable: target}, nil
}

// These indirections keep the package testable on every host without making
// platform selection configurable by users.
var runtimeGOOS = func() string { return runtime.GOOS }
var runtimeGOARCH = func() string { return runtime.GOARCH }

func archiveArchitecture(goarch string) (string, bool) {
	switch goarch {
	case "amd64", "arm64":
		return goarch, true
	default:
		return "", false
	}
}

func parseVersion(text string) (releaseVersion, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "v"))
	parts := strings.Split(text, ".")
	if len(parts) != 3 || text == "" {
		return releaseVersion{}, errors.New("expected major.minor.patch")
	}
	var values [3]int
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return releaseVersion{}, errors.New("invalid numeric version")
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return releaseVersion{}, errors.New("invalid numeric version")
		}
		values[i] = n
	}
	return releaseVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func formatVersion(v releaseVersion) string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func compareVersion(a, b releaseVersion) int {
	for _, pair := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func fetchRelease(client *http.Client, base string) (release, error) {
	data, err := fetchBytes(client, base+"/repos/"+repository+"/releases/latest", maxReleaseJSON)
	if err != nil {
		return release{}, fmt.Errorf("discover latest GitHub release: %w", err)
	}
	var info release
	if err := json.Unmarshal(data, &info); err != nil || strings.TrimSpace(info.TagName) == "" {
		return release{}, fmt.Errorf("GitHub latest release response is malformed")
	}
	return info, nil
}

func fetchBytes(client *http.Client, endpoint string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ticket-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds size limit")
	}
	return data, nil
}

func checksumFor(data []byte, wanted string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return "", errors.New("SHA256SUMS contains a malformed entry")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", errors.New("SHA256SUMS contains a malformed checksum")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == wanted {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no checksum for %s", wanted)
}

func extractExecutable(archivePath, dir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open release archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("read release archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var extracted string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read release archive: %w", err)
		}
		if path.Clean(strings.TrimPrefix(h.Name, "./")) != "ticket" || h.Typeflag != tar.TypeReg {
			return "", errors.New("release archive must contain only a regular ticket executable")
		}
		if h.Size < 1 || h.Size > maxExecutable {
			return "", errors.New("release executable has an invalid size")
		}
		extracted = filepath.Join(dir, "ticket")
		out, err := os.OpenFile(extracted, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
		if err != nil {
			return "", fmt.Errorf("extract release executable: %w", err)
		}
		_, copyErr := io.CopyN(out, tr, h.Size)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(extracted)
			if copyErr != nil {
				return "", fmt.Errorf("extract release executable: %w", copyErr)
			}
			return "", fmt.Errorf("extract release executable: %w", closeErr)
		}
	}
	if extracted == "" {
		return "", errors.New("release archive does not contain ticket")
	}
	return extracted, nil
}

func validateExecutable(filename, arch string) error {
	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("validate release executable: %w", err)
	}
	if info.Mode()&0o111 == 0 {
		return errors.New("release executable is not executable")
	}
	f, err := elf.Open(filename)
	if err != nil {
		return fmt.Errorf("release executable is not a Linux ELF binary: %w", err)
	}
	machineOK := (arch == "amd64" && f.Machine == elf.EM_X86_64) || (arch == "arm64" && f.Machine == elf.EM_AARCH64)
	f.Close()
	if !machineOK {
		return fmt.Errorf("release executable architecture does not match linux/%s", arch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), validationTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, filename, "--version").Run(); err != nil {
		return fmt.Errorf("release executable failed --version: %w", err)
	}
	return nil
}

func replaceExecutable(target, replacement string) error {
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("destination is not a regular file")
	}
	data, err := os.ReadFile(replacement)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".ticket-upgrade-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	return nil
}
