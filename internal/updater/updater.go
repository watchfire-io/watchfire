// Package updater checks for updates via GitHub Releases and replaces binaries.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/watchfire-io/watchfire/internal/buildinfo"
)

const (
	releasesURL = "https://api.github.com/repos/watchfire-io/watchfire/releases/latest"
)

// ReleaseInfo contains information about a GitHub release.
type ReleaseInfo struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a downloadable file in a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// UpdateResult contains the result of an update check.
type UpdateResult struct {
	Available      bool
	CurrentVersion string
	LatestVersion  string
	ReleaseURL     string
	Release        *ReleaseInfo
}

// CheckForUpdate queries GitHub Releases API for a newer version.
func CheckForUpdate() (*UpdateResult, error) {
	req, err := http.NewRequestWithContext(context.Background(), "GET", releasesURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "watchfire/"+buildinfo.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// No releases yet
		return &UpdateResult{
			Available:      false,
			CurrentVersion: buildinfo.Version,
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release ReleaseInfo
	if decodeErr := json.NewDecoder(resp.Body).Decode(&release); decodeErr != nil {
		return nil, fmt.Errorf("decode release: %w", decodeErr)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	current, err := ParseSemver(buildinfo.Version)
	if err != nil {
		// If current version is "dev" or unparseable, treat as older
		return &UpdateResult{
			Available:      true,
			CurrentVersion: buildinfo.Version,
			LatestVersion:  latestVersion,
			ReleaseURL:     release.HTMLURL,
			Release:        &release,
		}, nil
	}

	latest, err := ParseSemver(latestVersion)
	if err != nil {
		return nil, fmt.Errorf("parse latest version %q: %w", latestVersion, err)
	}

	return &UpdateResult{
		Available:      current.LessThan(latest),
		CurrentVersion: buildinfo.Version,
		LatestVersion:  latestVersion,
		ReleaseURL:     release.HTMLURL,
		Release:        &release,
	}, nil
}

// CLIAssetName returns the expected asset name for the CLI binary.
func CLIAssetName() string {
	name := fmt.Sprintf("watchfire-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// DaemonAssetName returns the expected asset name for the daemon binary.
func DaemonAssetName() string {
	name := fmt.Sprintf("watchfired-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// FindAsset finds an asset by name in a release.
func FindAsset(release *ReleaseInfo, name string) *Asset {
	for _, a := range release.Assets {
		if a.Name == name {
			return &a
		}
	}
	return nil
}

// DownloadAsset downloads a release asset and returns the path to the staged
// binary. The file is created inside preferDir whenever possible so that the
// subsequent os.Rename onto the final install path (see ReplaceBinary) is a
// same-filesystem, atomic operation — on Linux os.Rename reduces to
// renameat2(2), which returns EXDEV across filesystems (e.g. tmpfs /tmp vs
// ext4 ~/.local/bin on Fedora/Ubuntu). Callers should pass the directory that
// contains the final install path as preferDir. An empty preferDir or one
// that's not writable (read-only, permission denied) causes a fallback to
// the OS-default temp directory; ReplaceBinary handles the cross-filesystem
// case with an explicit copy+fsync+rename within the install dir.
func DownloadAsset(asset *Asset, preferDir string) (string, error) {
	dlReq, err := http.NewRequestWithContext(context.Background(), "GET", asset.BrowserDownloadURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return "", fmt.Errorf("download asset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmpFile, err := createStagingFile(preferDir)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	if _, copyErr := io.Copy(tmpFile, resp.Body); copyErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write temp file: %w", copyErr)
	}

	if syncErr := tmpFile.Sync(); syncErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("fsync temp file: %w", syncErr)
	}

	_ = tmpFile.Close()

	// Set exec perms before the rename so the final binary lands already
	// executable — no post-rename chmod window where a concurrent
	// `watchfire` invocation could see a non-executable file.
	if err := os.Chmod(tmpFile.Name(), 0o755); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("chmod temp file: %w", err)
	}

	return tmpFile.Name(), nil
}

// createStagingFile tries to create a temp file inside preferDir — the
// install directory — so a subsequent os.Rename to the final path is always
// same-filesystem. Falls back to os.TempDir() when preferDir is empty, does
// not exist, or is not writable by the current user.
func createStagingFile(preferDir string) (*os.File, error) {
	if preferDir != "" {
		if f, err := os.CreateTemp(preferDir, ".watchfire-update-*"); err == nil {
			return f, nil
		}
	}
	return os.CreateTemp("", "watchfire-update-*")
}

// ReplaceBinary atomically replaces a binary at destPath with the new binary
// at newPath. DownloadAsset tries to stage newPath inside filepath.Dir(destPath)
// so the rename below is a single same-filesystem operation. If that
// guarantee was broken (e.g. the install dir was briefly not writable at
// download time and DownloadAsset fell back to os.TempDir()), the new binary
// is first copy+fsync'd into the install dir and the final rename still
// happens within one directory — preserving atomicity end-to-end so a
// concurrent `watchfire` invocation never sees a half-written binary.
func ReplaceBinary(destPath, newPath string) error {
	destPath, err := filepath.EvalSymlinks(destPath)
	if err != nil {
		return fmt.Errorf("resolve symlink: %w", err)
	}
	destPath, err = filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}

	installDir := filepath.Dir(destPath)

	// Ensure the staged binary sits next to the install path. If
	// DownloadAsset was able to stage it into installDir already this is a
	// no-op and the fast path below is a single os.Rename.
	stagedPath, err := stageIntoInstallDir(newPath, installDir)
	if err != nil {
		return fmt.Errorf("stage new binary: %w", err)
	}

	bakPath := destPath + ".bak"

	// Remove any stale backup from a previous failed update.
	_ = os.Remove(bakPath)

	// Rename current → backup (same-filesystem, guaranteed atomic).
	if err := os.Rename(destPath, bakPath); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("backup old binary: %w", err)
	}

	// Move new → target (same-filesystem, guaranteed atomic).
	if err := os.Rename(stagedPath, destPath); err != nil {
		// Try to restore backup so the user keeps a working binary.
		_ = os.Rename(bakPath, destPath)
		_ = os.Remove(stagedPath)
		return fmt.Errorf("install new binary: %w", err)
	}

	// Clean up backup
	_ = os.Remove(bakPath)

	return nil
}

// stageIntoInstallDir guarantees that the staged binary lives inside
// installDir so ReplaceBinary's final rename is same-filesystem. If newPath
// is already there (the DownloadAsset fast path) it is returned unchanged.
// Otherwise newPath is copied+fsync'd into installDir and removed — this is
// the EXDEV fallback for the case where DownloadAsset had to write to
// os.TempDir() (e.g. installDir wasn't writable at download time).
func stageIntoInstallDir(newPath, installDir string) (string, error) {
	absNew, err := filepath.Abs(newPath)
	if err != nil {
		return "", err
	}
	if filepath.Dir(absNew) == installDir {
		return absNew, nil
	}

	src, err := os.Open(absNew)
	if err != nil {
		return "", fmt.Errorf("open staged binary: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.CreateTemp(installDir, ".watchfire-update-*")
	if err != nil {
		return "", fmt.Errorf("create staging file in install dir: %w", err)
	}
	dstPath := dst.Name()

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("copy staged binary: %w", err)
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("fsync staged binary: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("close staged binary: %w", err)
	}
	if err := os.Chmod(dstPath, 0o755); err != nil {
		_ = os.Remove(dstPath)
		return "", fmt.Errorf("chmod staged binary: %w", err)
	}

	// Remove the original temp file now that its contents live in installDir.
	_ = os.Remove(absNew)

	return dstPath, nil
}
