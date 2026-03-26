package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/chromedp"
)

// ---------------------------------------------------------------------------
// helper: set package-level arguments for tests
// ---------------------------------------------------------------------------

func setOutputPath(v string)  { arguments.outputPath = &v }
func setProfileDir(v string)  { arguments.profileDir = &v }
func setReUseProfile(v bool)  { arguments.reUseProfile = &v }
func setURLs(u ...string)     { urls = stringSlice(u) }

// ---------------------------------------------------------------------------
// stringSlice
// ---------------------------------------------------------------------------

func TestStringSlice_SetAndString(t *testing.T) {
	var ss stringSlice
	ss.Set("a")
	ss.Set("b")
	if got := ss.String(); got != "a, b" {
		t.Errorf("String() = %q, want %q", got, "a, b")
	}
}

// ---------------------------------------------------------------------------
// outputPath
// ---------------------------------------------------------------------------

func TestOutputPath_SingleURL(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "out.png")
	setOutputPath(outFile)
	setURLs("https://example.com")
	if got := outputPath(0); got != outFile {
		t.Errorf("outputPath(0) = %q, want %q", got, outFile)
	}
}

func TestOutputPath_MultipleURLs(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.png")
	setOutputPath(outFile)
	setURLs("https://a.com", "https://b.com", "https://c.com")

	tests := []struct {
		index int
		want  string
	}{
		{0, filepath.Join(dir, "out_001.png")},
		{1, filepath.Join(dir, "out_002.png")},
		{2, filepath.Join(dir, "out_003.png")},
	}
	for _, tt := range tests {
		if got := outputPath(tt.index); got != tt.want {
			t.Errorf("outputPath(%d) = %q, want %q", tt.index, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// chromeProfileCacheRoot
// ---------------------------------------------------------------------------

func TestChromeProfileCacheRoot_EnvVar(t *testing.T) {
	t.Setenv("SITESNAP_CACHE_DIR", "/custom/cache")
	if got := chromeProfileCacheRoot(); got != "/custom/cache" {
		t.Errorf("chromeProfileCacheRoot() = %q, want /custom/cache", got)
	}
}

func TestChromeProfileCacheRoot_Default(t *testing.T) {
	t.Setenv("SITESNAP_CACHE_DIR", "")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".sitesnap")
	if got := chromeProfileCacheRoot(); got != want {
		t.Errorf("chromeProfileCacheRoot() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// removeStaleChromeLocks
// ---------------------------------------------------------------------------

func TestRemoveStaleChromeLocks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		os.WriteFile(filepath.Join(dir, name), []byte{}, 0644)
	}
	removeStaleChromeLocks(dir)
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("lock file %s should have been removed", name)
		}
	}
}

// ---------------------------------------------------------------------------
// setupProfileCache
// ---------------------------------------------------------------------------

func TestSetupProfileCache_EmptyProfileDir(t *testing.T) {
	setProfileDir("")
	if got := setupProfileCache(); got != "" {
		t.Errorf("setupProfileCache() = %q, want empty string", got)
	}
}

func TestSetupProfileCache_CopiesProfileToTempDir(t *testing.T) {
	// Create a fake profile source directory with a marker file
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "Cookies"), []byte("data"), 0644)

	setProfileDir(srcDir)
	setReUseProfile(false)

	got := setupProfileCache()
	defer os.RemoveAll(got)

	profileName := filepath.Base(srcDir)
	// Should be a temp directory, not under chromeProfileCacheRoot
	if got == "" {
		t.Fatal("setupProfileCache() returned empty string")
	}
	// Verify marker file was copied
	if _, err := os.Stat(filepath.Join(got, profileName, "Cookies")); err != nil {
		t.Errorf("Cookies file should exist in cached profile: %v", err)
	}
}

func TestSetupProfileCache_ReUseExisting(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("SITESNAP_CACHE_DIR", cacheRoot)

	srcDir := t.TempDir()
	setProfileDir(srcDir)
	setReUseProfile(true)

	// Pre-create cached profile
	profileName := filepath.Base(srcDir)
	cachedDir := filepath.Join(cacheRoot, "userdata-"+profileName, profileName)
	os.MkdirAll(cachedDir, 0700)
	os.WriteFile(filepath.Join(cachedDir, "Marker"), []byte("cached"), 0644)

	got := setupProfileCache()
	wantDir := filepath.Join(cacheRoot, "userdata-"+profileName)
	if got != wantDir {
		t.Fatalf("setupProfileCache() = %q, want %q", got, wantDir)
	}
	// Marker should still exist (not re-copied)
	if _, err := os.Stat(filepath.Join(cachedDir, "Marker")); err != nil {
		t.Error("Marker should still exist in reuse mode")
	}
}

// ---------------------------------------------------------------------------
// cleanupProfileCache
// ---------------------------------------------------------------------------

func TestCleanupProfileCache_EmptyDir(t *testing.T) {
	// Should not panic with empty string
	setReUseProfile(false)
	cleanupProfileCache("")
}

func TestCleanupProfileCache_DeletesWhenNotReuse(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "test")
	os.WriteFile(marker, []byte("x"), 0644)

	setReUseProfile(false)
	cleanupProfileCache(dir)

	if _, err := os.Stat(dir); err == nil {
		t.Error("cache dir should have been deleted")
	}
}

func TestCleanupProfileCache_KeepsWhenReuse(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "test")
	os.WriteFile(marker, []byte("x"), 0644)

	setReUseProfile(true)
	cleanupProfileCache(dir)

	if _, err := os.Stat(marker); err != nil {
		t.Error("cache dir should have been kept in reuse mode")
	}
}

// ---------------------------------------------------------------------------
// E2E tests (Chrome required)
//
// Run manually:
//   SITESNAP_E2E=1 go test -v -run TestE2E
// ---------------------------------------------------------------------------

func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("SITESNAP_E2E") == "" {
		t.Skip("Skipping E2E test; set SITESNAP_E2E=1 to run")
	}
}

func TestE2E_ViewportScreenshot(t *testing.T) {
	skipUnlessE2E(t)

	outFile := filepath.Join(t.TempDir(), "viewport.png")
	setOutputPath(outFile)
	setURLs("https://www.example.com")
	setProfileDir("")

	w := int64(1280)
	h := int64(860)
	ws := 1
	f := false
	d := false
	n := false
	arguments.windowWidth = &w
	arguments.windowHeight = &h
	deviceScaleFactor = 2.0
	arguments.waitSeconds = &ws
	arguments.fullScreenshot = &f
	arguments.debug = &d
	arguments.noHeadless = &n
	arguments.querySelector = strPtr("")
	chromeFlags = nil

	browserCtx, shutdown := newBrowserContext("")
	defer shutdown()
	if err := chromedp.Run(browserCtx); err != nil {
		t.Fatalf("failed to start browser: %v", err)
	}

	tabCtx, tabCancel := chromedp.NewContext(browserCtx)
	defer tabCancel()

	buf, err := takeScreenshot(tabCtx, "https://www.example.com")
	if err != nil {
		t.Fatalf("takeScreenshot failed: %v", err)
	}
	if len(buf) == 0 {
		t.Fatal("screenshot buffer is empty")
	}
	if err := os.WriteFile(outFile, buf, 0644); err != nil {
		t.Fatalf("failed to write screenshot: %v", err)
	}
	info, _ := os.Stat(outFile)
	t.Logf("screenshot saved: %s (%d bytes)", outFile, info.Size())
}

func TestE2E_FullPageScreenshot(t *testing.T) {
	skipUnlessE2E(t)

	outFile := filepath.Join(t.TempDir(), "fullpage.png")
	setOutputPath(outFile)
	setURLs("https://www.example.com")
	setProfileDir("")

	w := int64(1280)
	h := int64(860)
	ws := 1
	f := true
	d := false
	n := false
	arguments.windowWidth = &w
	arguments.windowHeight = &h
	deviceScaleFactor = 2.0
	arguments.waitSeconds = &ws
	arguments.fullScreenshot = &f
	arguments.debug = &d
	arguments.noHeadless = &n
	arguments.querySelector = strPtr("")
	chromeFlags = nil

	browserCtx, shutdown := newBrowserContext("")
	defer shutdown()
	if err := chromedp.Run(browserCtx); err != nil {
		t.Fatalf("failed to start browser: %v", err)
	}

	tabCtx, tabCancel := chromedp.NewContext(browserCtx)
	defer tabCancel()

	buf, err := takeScreenshot(tabCtx, "https://www.example.com")
	if err != nil {
		t.Fatalf("takeScreenshot failed: %v", err)
	}
	if len(buf) == 0 {
		t.Fatal("screenshot buffer is empty")
	}
	t.Logf("full-page screenshot: %d bytes", len(buf))
}

func strPtr(s string) *string { return &s }
