package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseAndCompareVersions(t *testing.T) {
	tests := []struct {
		text string
		want releaseVersion
		ok   bool
	}{
		{"0.1.0", releaseVersion{0, 1, 0}, true},
		{"v12.3.4", releaseVersion{12, 3, 4}, true},
		{"1.2", releaseVersion{}, false},
		{"1.2.3-rc1", releaseVersion{}, false},
		{"01.2.3", releaseVersion{}, false},
		{"1.2.x", releaseVersion{}, false},
	}
	for _, tt := range tests {
		got, err := parseVersion(tt.text)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseVersion(%q) = %v, %v; want %v, ok=%v", tt.text, got, err, tt.want, tt.ok)
		}
	}
	low, _ := parseVersion("1.2.3")
	high, _ := parseVersion("1.3.0")
	if compareVersion(high, low) <= 0 || compareVersion(low, high) >= 0 || compareVersion(low, low) != 0 {
		t.Fatal("version comparison is incorrect")
	}
}

func TestArchiveArchitecture(t *testing.T) {
	if got, ok := archiveArchitecture("amd64"); !ok || got != "amd64" {
		t.Fatalf("amd64 mapping = %q, %v", got, ok)
	}
	if got, ok := archiveArchitecture("arm64"); !ok || got != "arm64" {
		t.Fatalf("arm64 mapping = %q, %v", got, ok)
	}
	if _, ok := archiveArchitecture("386"); ok {
		t.Fatal("386 should be unsupported")
	}
}

func TestRunRejectsUnsupportedPlatformAndVersion(t *testing.T) {
	if _, err := Run(Options{CurrentVersion: "0.1.0", GOOS: "darwin", GOARCH: "amd64"}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("darwin error = %v", err)
	}
	if _, err := Run(Options{CurrentVersion: "0.1.0", GOOS: "linux", GOARCH: "386"}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("386 error = %v", err)
	}
	if _, err := Run(Options{CurrentVersion: "development", GOOS: "linux", GOARCH: "amd64"}); err == nil || !strings.Contains(err.Error(), "usable release version") {
		t.Fatalf("development version error = %v", err)
	}
}

func TestRunRejectsMalformedReleaseResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testResponse(`{"tag_name":"v0.1"}`), nil
	})}
	_, err := Run(Options{CurrentVersion: "0.1.0", GOOS: "linux", GOARCH: "amd64", APIBaseURL: "https://api.test", HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "unusable stable release") {
		t.Fatalf("malformed release error = %v", err)
	}
}

func TestRunAlreadyLatestDoesNotDownload(t *testing.T) {
	var paths []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/repos/toolsupply/ticket/releases/latest" {
			t.Fatalf("unexpected download: %s", r.URL.Path)
		}
		return testResponse(`{"tag_name":"v0.1.0","draft":false,"prerelease":false}`), nil
	})}
	result, err := Run(Options{CurrentVersion: "0.1.0", GOOS: "linux", GOARCH: "amd64", APIBaseURL: "https://api.test", DownloadBaseURL: "https://download.test", HTTPClient: client})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Changed || result.OldVersion != "0.1.0" || result.NewVersion != "0.1.0" {
		t.Fatalf("result = %+v", result)
	}
	if len(paths) != 1 {
		t.Fatalf("paths = %v", paths)
	}
}

func TestRunVerifiesChecksumBeforeReplacement(t *testing.T) {
	old := []byte("old executable")
	target := filepath.Join(t.TempDir(), "ticket")
	if err := os.WriteFile(target, old, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := testArchive(t, []byte("not an ELF"))
	client := releaseClient(t, "v0.1.1", archive, strings.Repeat("0", 64), "amd64")
	_, err := Run(Options{CurrentVersion: "0.1.0", GOOS: "linux", GOARCH: "amd64", ExecutablePath: target, APIBaseURL: "https://api.test", DownloadBaseURL: "https://download.test/download", HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum error = %v", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || !bytes.Equal(got, old) {
		t.Fatalf("target after checksum failure = %q, %v", got, readErr)
	}
}

func TestRunUpdatesFromLocalRelease(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux executable validation is platform-specific")
	}
	tool, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Skip("test host has no /bin/true")
	}
	target := filepath.Join(t.TempDir(), "ticket")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := testArchive(t, tool)
	hash := sha256.Sum256(archive)
	client := releaseClient(t, "v0.1.1", archive, hex.EncodeToString(hash[:]), runtime.GOARCH)
	result, err := Run(Options{CurrentVersion: "0.1.0", GOOS: "linux", GOARCH: runtime.GOARCH, ExecutablePath: target, APIBaseURL: "https://api.test", DownloadBaseURL: "https://download.test/download", HTTPClient: client})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Changed || result.OldVersion != "0.1.0" || result.NewVersion != "0.1.1" {
		t.Fatalf("result = %+v", result)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, tool) {
		t.Fatalf("replacement = %v, %v", err, bytes.Equal(got, tool))
	}
}

func TestChecksumFor(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if got, err := checksumFor([]byte(hash+"  ticket-linux-amd64.tar.gz\n"), "ticket-linux-amd64.tar.gz"); err != nil || got != hash {
		t.Fatalf("checksum = %q, %v", got, err)
	}
	for _, input := range []string{
		"not-a-checksum  ticket-linux-amd64.tar.gz\n",
		strings.Repeat("a", 64) + "\n",
		strings.Repeat("a", 64) + "  other.tar.gz\n",
	} {
		if _, err := checksumFor([]byte(input), "ticket-linux-amd64.tar.gz"); err == nil {
			t.Fatalf("accepted invalid checksum input %q", input)
		}
	}
}

func TestExtractRejectsUnexpectedArchiveEntries(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	data := newTar(t, "other", []byte("content"))
	if err := os.WriteFile(archive, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractExecutable(archive, filepath.Dir(archive)); err == nil {
		t.Fatal("unexpected archive entry was accepted")
	}
}

func TestReplaceFailurePreservesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ticket")
	if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(target, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing replacement was accepted")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "original" {
		t.Fatalf("target changed after replacement failure: %q, %v", got, err)
	}
}

func TestReplaceRejectsNonRegularDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ticket")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(target, replacement); err == nil {
		t.Fatal("directory destination was accepted")
	}
}

func releaseClient(t *testing.T, tag string, archive []byte, checksum, arch string) *http.Client {
	t.Helper()
	archiveName := "ticket-linux-" + arch + ".tar.gz"
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/repos/toolsupply/ticket/releases/latest":
			return testResponse(fmt.Sprintf(`{"tag_name":%q,"draft":false,"prerelease":false}`, tag)), nil
		case "/download/" + tag + "/SHA256SUMS":
			return testResponse(fmt.Sprintf("%s  %s\n", checksum, archiveName)), nil
		case "/download/" + tag + "/" + archiveName:
			return testResponseBytes(archive), nil
		default:
			return testResponseStatus("not found", http.StatusNotFound), nil
		}
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testResponse(body string) *http.Response {
	return testResponseBytes([]byte(body))
}

func testResponseBytes(body []byte) *http.Response {
	return testResponseStatus(string(body), http.StatusOK)
}

func testResponseStatus(body string, status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d", status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testArchive(t *testing.T, executable []byte) []byte {
	t.Helper()
	return newTar(t, "ticket", executable)
}

func newTar(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarWriter := tar.NewWriter(gz)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
