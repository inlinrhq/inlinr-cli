package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/release"
)

const (
	githubReleasesAPI     = "https://api.github.com/repos/inlinrhq/inlinr-cli/releases/latest"
	githubReleaseDownload = "https://github.com/inlinrhq/inlinr-cli/releases/download"
)

// runUpgrade checks the latest GitHub release, downloads the matching
// binary, SHA256SUMS.txt and its signature, checks the signature against the
// release key built into this binary, then the hash, and atomically replaces
// the running binary (Windows-safe via rename-then-write).
func runUpgrade(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	check := fs.Bool("check", false, "print the latest version without installing")
	force := fs.Bool("force", false, "reinstall even if already on the latest version")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	latest, err := fetchLatestTag(ctx)
	if err != nil {
		return fmt.Errorf("check latest release: %w", err)
	}
	fmt.Printf("current: %s  latest: %s\n", Version, latest)

	if *check {
		if latest == "v"+Version || latest == Version {
			fmt.Println("up to date")
		} else {
			fmt.Println("update available — run 'inlinr upgrade' to install")
		}
		return nil
	}

	if !*force && (latest == "v"+Version || latest == Version) {
		fmt.Println("already up to date")
		return nil
	}

	binName := platformBinaryName()
	binURL := fmt.Sprintf("%s/%s/%s", githubReleaseDownload, latest, binName)
	shaURL := fmt.Sprintf("%s/%s/SHA256SUMS.txt", githubReleaseDownload, latest)

	fmt.Printf("downloading %s...\n", binURL)
	binBytes, err := fetchBytes(ctx, binURL)
	if err != nil {
		return err
	}
	shaBytes, err := fetchBytes(ctx, shaURL)
	if err != nil {
		return err
	}
	sigBytes, err := fetchBytes(ctx, shaURL+".sig")
	if err != nil {
		return fmt.Errorf("release %s is not signed, not installing it: %w", latest, err)
	}
	if err := release.VerifySums(shaBytes, sigBytes); err != nil {
		return fmt.Errorf("release %s: %w", latest, err)
	}
	fmt.Println("signature verified")

	expected := parseSHASum(string(shaBytes), binName)
	if expected == "" {
		return fmt.Errorf("no SHA256 entry for %s in SHA256SUMS.txt", binName)
	}
	actual := sha256Hex(binBytes)
	if expected != actual {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, actual)
	}
	fmt.Println("sha256 verified")

	if err := swapBinary(binBytes); err != nil {
		return err
	}
	fmt.Printf("upgraded to %s\n", latest)
	return nil
}

func fetchLatestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "inlinr-cli/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from GitHub API", resp.StatusCode)
	}
	var parsed struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.TagName == "" {
		return "", errors.New("empty tag_name in GitHub response")
	}
	return parsed.TagName, nil
}

func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "inlinr-cli/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func platformBinaryName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("inlinr-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

func parseSHASum(manifest, name string) string {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[1] == name {
			return fields[0]
		}
	}
	return ""
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// swapBinary replaces the running executable. The new bytes are written next
// to it first, so the only gap is a rename: a plugin spawning `inlinr` while
// an upgrade runs used to find no binary at all for as long as the write took,
// and lost the beats it was flushing. On Windows a running .exe cannot be
// replaced but can be renamed, so it moves to ".old" first; that file stays
// until the next upgrade (the running process can't remove itself).
func swapBinary(newBytes []byte) error {
	current, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	return replaceFile(current, newBytes, runtime.GOOS == "windows")
}

func replaceFile(current string, newBytes []byte, renameAway bool) error {
	next := current + ".new"
	if err := os.WriteFile(next, newBytes, 0o755); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("write new binary: %w", err)
	}
	if renameAway {
		old := current + ".old"
		_ = os.Remove(old) // leftover from previous upgrade
		if err := os.Rename(current, old); err != nil {
			_ = os.Remove(next)
			return fmt.Errorf("rename current: %w", err)
		}
		if err := os.Rename(next, current); err != nil {
			// Put the old one back so the user isn't left without a binary.
			_ = os.Rename(old, current)
			_ = os.Remove(next)
			return fmt.Errorf("move new binary: %w", err)
		}
		return nil
	}
	// Elsewhere a rename over the running file is atomic.
	if err := os.Rename(next, current); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("move new binary: %w", err)
	}
	return nil
}
