package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const browserExecutableEnvironment = "MUSIC2BB_BROWSER_EXECUTABLE"

type browserDiscovery struct {
	getenv   func(string) string
	lookPath func(string) (string, error)
}

func defaultBrowserDiscovery() browserDiscovery {
	return browserDiscovery{getenv: os.Getenv, lookPath: exec.LookPath}
}

func resolveSystemBrowser(platform, override string, discovery browserDiscovery) (string, error) {
	if discovery.getenv == nil {
		discovery.getenv = os.Getenv
	}
	if discovery.lookPath == nil {
		discovery.lookPath = exec.LookPath
	}
	if candidate := strings.TrimSpace(override); candidate != "" {
		return validateBrowserExecutable(candidate, platform)
	}
	if candidate := strings.TrimSpace(discovery.getenv(browserExecutableEnvironment)); candidate != "" {
		return validateBrowserExecutable(candidate, platform)
	}

	goos, _, ok := strings.Cut(platform, "/")
	if !ok {
		return "", nil
	}
	for _, name := range browserNames(goos) {
		candidate, err := discovery.lookPath(name)
		if err != nil {
			continue
		}
		if resolved, err := validateBrowserExecutable(candidate, platform); err == nil {
			return resolved, nil
		}
	}
	for _, candidate := range conventionalBrowserPaths(goos, discovery.getenv) {
		if resolved, err := validateBrowserExecutable(candidate, platform); err == nil {
			return resolved, nil
		}
	}
	return "", nil
}

func validateBrowserExecutable(path, platform string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", &Error{Kind: ErrorInvalidExecutable, Op: "resolve executable", Err: err}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &Error{Kind: ErrorInvalidExecutable, Op: "resolve executable", Err: err}
	}
	if info.IsDir() {
		return "", &Error{Kind: ErrorInvalidExecutable, Op: "resolve executable", Err: fmt.Errorf("%s is a directory", abs)}
	}
	if !strings.HasPrefix(platform, "windows/") && info.Mode().Perm()&0o111 == 0 {
		return "", &Error{Kind: ErrorInvalidExecutable, Op: "resolve executable", Err: fmt.Errorf("%s is not executable", abs)}
	}
	return filepath.Clean(abs), nil
}

func browserNames(goos string) []string {
	switch goos {
	case "windows":
		return []string{"chromium.exe", "chrome.exe", "google-chrome.exe"}
	case "darwin", "linux":
		return []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"}
	default:
		return nil
	}
}

func conventionalBrowserPaths(goos string, getenv func(string) string) []string {
	switch goos {
	case "darwin":
		paths := []string{
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
		if home := strings.TrimSpace(getenv("HOME")); home != "" {
			paths = append(paths,
				filepath.Join(home, "Applications/Chromium.app/Contents/MacOS/Chromium"),
				filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
			)
		}
		return paths
	case "windows":
		var paths []string
		for _, root := range []string{getenv("LOCALAPPDATA"), getenv("PROGRAMFILES"), getenv("PROGRAMFILES(X86)")} {
			if strings.TrimSpace(root) == "" {
				continue
			}
			paths = append(paths,
				filepath.Join(root, "Chromium", "Application", "chrome.exe"),
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			)
		}
		return paths
	case "linux":
		return []string{
			"/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable", "/snap/bin/chromium",
			"/usr/lib/chromium/chromium", "/usr/lib/chromium-browser/chromium-browser",
			"/opt/google/chrome/chrome",
		}
	default:
		return nil
	}
}
