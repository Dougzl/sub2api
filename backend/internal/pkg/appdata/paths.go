package appdata

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultDataDirName       = "data"
	defaultSQLiteDBFilename  = "sub2api.db"
	defaultLogDirName        = "logs"
	defaultLogFilename       = "sub2api.log"
	defaultPublicOverrideDir = "public"
)

func ResolveDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		return cleanPath(dir)
	}

	if runtime.GOOS != "windows" {
		if dir := "/app/data"; isWritableDir(dir) {
			return dir
		}
	}

	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		return filepath.Join(filepath.Dir(exe), defaultDataDirName)
	}

	return cleanPath(filepath.Join(".", defaultDataDirName))
}

func DefaultSQLiteDBPath() string {
	return filepath.Join(ResolveDataDir(), defaultSQLiteDBFilename)
}

func DefaultLogFilePath() string {
	return filepath.Join(ResolveDataDir(), defaultLogDirName, defaultLogFilename)
}

func DefaultPublicOverrideDir() string {
	return filepath.Join(ResolveDataDir(), defaultPublicOverrideDir)
}

func ResolvePathInDataDir(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ResolveDataDir()
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}

	cleaned := filepath.Clean(filepath.FromSlash(raw))
	prefix := "data" + string(filepath.Separator)
	switch cleaned {
	case ".", "", "data":
		return ResolveDataDir()
	}
	if strings.HasPrefix(cleaned, "."+string(filepath.Separator)) {
		cleaned = strings.TrimPrefix(cleaned, "."+string(filepath.Separator))
	}
	if cleaned == "data" {
		return ResolveDataDir()
	}
	if strings.HasPrefix(cleaned, prefix) {
		cleaned = strings.TrimPrefix(cleaned, prefix)
	}
	return filepath.Join(ResolveDataDir(), cleaned)
}

func ResolvePathWithConfigBase(configFile, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ResolveDataDir()
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}

	cleaned := filepath.Clean(filepath.FromSlash(raw))
	if strings.HasPrefix(cleaned, "."+string(filepath.Separator)) {
		cleaned = strings.TrimPrefix(cleaned, "."+string(filepath.Separator))
	}

	if cleaned == "" || cleaned == "." {
		return ResolveDataDir()
	}

	dataPrefix := "data" + string(filepath.Separator)
	if cleaned == "data" || strings.HasPrefix(cleaned, dataPrefix) {
		return ResolvePathInDataDir(cleaned)
	}

	if strings.TrimSpace(configFile) != "" {
		return filepath.Join(filepath.Dir(filepath.Clean(configFile)), cleaned)
	}

	return ResolvePathInDataDir(cleaned)
}

func cleanPath(path string) string {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func isWritableDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	testFile := filepath.Join(dir, ".write_test")
	f, err := os.Create(testFile)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(testFile)
	return true
}
