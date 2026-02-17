// Package updater checks for updates via GitHub Releases and replaces binaries.
package updater

import (
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
	req, err := http.NewRequest("GET", releasesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "watchfire/"+buildinfo.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()

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
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
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
	return fmt.Sprintf("watchfire-darwin-%s", runtime.GOARCH)
}

// DaemonAssetName returns the expected asset name for the daemon binary.
func DaemonAssetName() string {
	return fmt.Sprintf("watchfired-darwin-%s", runtime.GOARCH)
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

// DownloadAsset downloads a release asset to a temp file and returns the path.
func DownloadAsset(asset *Asset) (string, error) {
	resp, err := http.Get(asset.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "watchfire-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write temp file: %w", err)
	}

	tmpFile.Close()

	// Make executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("chmod temp file: %w", err)
	}

	return tmpFile.Name(), nil
}

// ReplaceBinary atomically replaces a binary at destPath with a new binary at newPath.
func ReplaceBinary(destPath, newPath string) error {
	destPath, err := filepath.EvalSymlinks(destPath)
	if err != nil {
		return fmt.Errorf("resolve symlink: %w", err)
	}

	bakPath := destPath + ".bak"

	// Remove any stale backup
	os.Remove(bakPath)

	// Rename current → backup
	if err := os.Rename(destPath, bakPath); err != nil {
		return fmt.Errorf("backup old binary: %w", err)
	}

	// Move new → target
	if err := os.Rename(newPath, destPath); err != nil {
		// Try to restore backup
		_ = os.Rename(bakPath, destPath)
		return fmt.Errorf("install new binary: %w", err)
	}

	// Clean up backup
	os.Remove(bakPath)

	return nil
}
