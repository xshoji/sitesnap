package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"html"
	"image"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// stringSlice implements flag.Value to accept multiple -u flags.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// version is set at build time via ldflags.
var version = "dev"

const (
	Req = "\x1b[33m(required)\x1b[0m "
	// maxPhysicalDim is Chrome's GPU texture limit in physical pixels.
	// The actual CSS pixel limit depends on deviceScaleFactor.
	maxPhysicalDim int64 = 16384
)

var (
	commandDescription = "A fast, multi-page screenshot tool that requires only Chrome. Supports profile specification without locking your main browser.\n  Set SITESNAP_CACHE_DIR to override the default profile cache directory (~/.sitesnap).\n  Device scale factor can be changed via -c \"device-scale-factor=1.0\" (default: 2.0 Retina).\n  Custom DNS resolution via -c \"host-resolver-rules=MAP example.com 127.0.0.1\"."
	urls               stringSlice
	chromeFlags        stringSlice
	deviceScaleFactor  = 2.0
	arguments          = struct {
		outputPath     *string
		querySelector  *string
		profileDir     *string
		waitSeconds    *int
		windowWidth    *int64
		windowHeight   *int64
		fullScreenshot *bool
		showAddressBar *bool
		debug          *bool
		noHeadless     *bool
		reUseProfile   *bool
		parallel       *int
	}{
		flag.String("o", "" /*    */, Req+"Output path of screenshot (with multiple URLs, auto-numbered: <base>_001.png, _002.png, ...)"),
		flag.String("q", "" /*    */, "Query selector. Screenshot the first matching element. ( e.g. -q=\".className#id\" )"),
		flag.String("p", "" /*    */, "Chrome profile directory to copy. (e.g. -p=\"~/Library/Application Support/Google/Chrome/Default\")."),
		flag.Int("w", 3 /*        */, "Wait seconds after page navigation before taking screenshot"),
		flag.Int64("wi", 1280 /*  */, "Viewport width (affects page layout, e.g. responsive design). Without -q, this is the output image width"),
		flag.Int64("he", 860 /*   */, "Viewport height (affects page layout, e.g. responsive design). Without -q, this is the output image height"),
		flag.Bool("f", false /*   */, "\nEnable full screenshot mode"),
		flag.Bool("b", false /*   */, "\nAdd browser-style address bar to the top of screenshot"),
		flag.Bool("d", false /*   */, "\nEnable debug mode"),
		flag.Bool("n", false /*   */, "\nDisable headless mode"),
		flag.Bool("r", false /*   */, "\nReuse cached profile (do not delete after execution)"),
		flag.Int("t", runtime.NumCPU(), "Max number of parallel tabs for screenshot capture"),
	}
)

func init() {
	flag.Var(&urls, "u", Req+"URL (can be specified multiple times, e.g. -u \"https://xxxx/\" -u \"https://yyyy/\")")
	flag.Var(&chromeFlags, "c", "Extra Chrome flag as key=value (can be specified multiple times, e.g. -c \"lang=ja\" -c \"disable-extensions\").")
	flag.Usage = customUsage(commandDescription)
}

func main() {
	flag.Parse()
	if len(urls) == 0 || *arguments.outputPath == "" {
		log.Println("Error: -u:URLs and -o:output-path are required.")
		flag.Usage()
		os.Exit(0)
	}

	// Parse device-scale-factor from -c flags (e.g. -c "device-scale-factor=1.5")
	for _, cf := range chromeFlags {
		k, v, _ := strings.Cut(cf, "=")
		if k == "device-scale-factor" && v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				deviceScaleFactor = f
			} else {
				log.Fatalf("invalid device-scale-factor value: %s", v)
			}
		}
	}

	// --- 1. Profile cache setup ---
	profileCacheDir := setupProfileCache()

	// --- 2. Browser context ---
	browserCtx, shutdownBrowser := newBrowserContext(profileCacheDir)
	cleanup := func() {
		shutdownBrowser()
		cleanupProfileCache(profileCacheDir)
	}
	defer cleanup()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("signal received: %v, shutting down...", sig)
		cleanup()
		os.Exit(0)
	}()

	// --- 3. Start browser (must be done before parallel tabs) ---
	if err := chromedp.Run(browserCtx); err != nil {
		log.Fatal(err)
	}

	// --- 4. Log settings ---
	logSettings(profileCacheDir)

	// --- 5. Take screenshots ---
	if len(urls) == 1 {
		// Single URL: use the browser's initial tab directly
		log.Printf("[1/1] capturing: %s", urls[0])
		buf, err := takeScreenshot(browserCtx, urls[0])
		if err != nil {
			log.Fatalf("capture %s: %v", urls[0], err)
		}
		outPath := outputPath(0)
		if dir := filepath.Dir(outPath); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Fatalf("create dir %s: %v", dir, err)
			}
		}
		if err := os.WriteFile(outPath, buf, 0644); err != nil {
			log.Fatalf("write %s: %v", outPath, err)
		}
		log.Printf("saved screenshot: %s", outPath)
	} else {
		// Multiple URLs: parallel capture with separate tabs
		type result struct {
			index int
			err   error
		}
		results := make(chan result, len(urls))
		sem := make(chan struct{}, *arguments.parallel)
		for i, u := range urls {
			go func(i int, u string) {
				sem <- struct{}{}
				defer func() { <-sem }()

				tabCtx, tabCancel := chromedp.NewContext(browserCtx)
				defer tabCancel()

				log.Printf("[%d/%d] capturing: %s", i+1, len(urls), u)
				buf, err := takeScreenshot(tabCtx, u)
				if err != nil {
					results <- result{i, fmt.Errorf("capture %s: %w", u, err)}
					return
				}

				outPath := outputPath(i)
				if dir := filepath.Dir(outPath); dir != "." {
					if err := os.MkdirAll(dir, 0755); err != nil {
						results <- result{i, fmt.Errorf("create dir %s: %w", dir, err)}
						return
					}
				}
				if err := os.WriteFile(outPath, buf, 0644); err != nil {
					results <- result{i, fmt.Errorf("write %s: %w", outPath, err)}
					return
				}
				log.Printf("saved screenshot: %s", outPath)
				results <- result{i, nil}
			}(i, u)
		}
		for range urls {
			if r := <-results; r.err != nil {
				log.Fatal(r.err)
			}
		}
	}
}

// outputPath returns the output file path for the i-th URL.
// Single URL: uses -o as-is. Multiple URLs: <base>_001.png, _002.png, ...
func outputPath(index int) string {
	if len(urls) == 1 {
		return *arguments.outputPath
	}
	ext := filepath.Ext(*arguments.outputPath)
	base := strings.TrimSuffix(*arguments.outputPath, ext)
	return fmt.Sprintf("%s_%03d%s", base, index+1, ext)
}

// setupProfileCache copies the specified Chrome profile to a cache directory.
// Returns the cache directory path to clean up later (empty string if no profile specified).
//
// With -r (reuse): uses a persistent directory under chromeProfileCacheRoot()
// Without -r:      uses a temporary directory (os.MkdirTemp), deleted on exit
//
// Cache structure:
//
//	<cacheRoot>/userdata-<profileName>/               <- user-data-dir
//	<cacheRoot>/userdata-<profileName>/<profileName>/ <- copied profile data
func setupProfileCache() string {
	if *arguments.profileDir == "" {
		return ""
	}

	profileName := filepath.Base(*arguments.profileDir)

	if *arguments.reUseProfile {
		// Reuse mode: persistent directory under home (or env var)
		userDataDir := filepath.Join(chromeProfileCacheRoot(), "userdata-"+profileName)
		profileSubDir := filepath.Join(userDataDir, profileName)
		if _, err := os.Stat(profileSubDir); err == nil {
			log.Printf("reuse cached profile: %s", profileSubDir)
			return userDataDir
		}
		if err := os.MkdirAll(userDataDir, 0700); err != nil {
			log.Fatalf("failed to create cache dir: %v", err)
		}
		if err := os.CopyFS(profileSubDir, os.DirFS(*arguments.profileDir)); err != nil {
			log.Fatalf("failed to copy profile: %v", err)
		}
		log.Printf("copied profile: %s -> %s", *arguments.profileDir, profileSubDir)
		return userDataDir
	}

	// Non-reuse mode: use a temporary directory
	userDataDir, err := os.MkdirTemp("", "sitesnap-userdata-")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}
	profileSubDir := filepath.Join(userDataDir, profileName)
	if err := os.CopyFS(profileSubDir, os.DirFS(*arguments.profileDir)); err != nil {
		log.Fatalf("failed to copy profile: %v", err)
	}
	log.Printf("copied profile: %s -> %s", *arguments.profileDir, profileSubDir)
	return userDataDir
}

// newBrowserContext creates a chromedp browser context with the configured options.
// userDataDir is the profile cache directory returned by setupProfileCache (empty if no profile).
// Returns the context and a shutdown function that can be called multiple times safely.
func newBrowserContext(userDataDir string) (context.Context, func()) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !*arguments.noHeadless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)

	// Apply extra Chrome flags from -c options
	for _, cf := range chromeFlags {
		k, v, _ := strings.Cut(cf, "=")
		if v == "" {
			opts = append(opts, chromedp.Flag(k, true))
		} else {
			opts = append(opts, chromedp.Flag(k, v))
		}
	}

	if userDataDir != "" {
		profileName := filepath.Base(*arguments.profileDir)
		removeStaleChromeLocks(userDataDir)
		opts = append(opts,
			chromedp.Flag("user-data-dir", userDataDir),
			chromedp.Flag("profile-directory", profileName),
			chromedp.Flag("use-mock-keychain", false),
			chromedp.Flag("password-store", "keychain"),
		)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	var ctxOpts []chromedp.ContextOption
	if *arguments.debug {
		ctxOpts = append(ctxOpts, chromedp.WithDebugf(log.Printf))
	} else {
		ctxOpts = append(ctxOpts, chromedp.WithLogf(log.Printf))
	}

	browserCtx, browserCancel := chromedp.NewContext(allocCtx, ctxOpts...)

	var once sync.Once
	shutdown := func() {
		once.Do(func() {
			// Close all tabs via CDP before shutting down to keep the session clean
			closeAllTargets(browserCtx)
			// Gracefully close Chrome via CDP to avoid "didn't shut down correctly" warning
			if err := chromedp.Cancel(browserCtx); err != nil {
				log.Printf("graceful browser close failed: %v", err)
			}
			browserCancel()
			allocCancel()
		})
	}

	return browserCtx, shutdown
}

// takeScreenshot navigates to the URL and captures a screenshot.
// All chromedp actions run in a single Run call to avoid race conditions
// when multiple tabs operate concurrently.
func takeScreenshot(ctx context.Context, url string) ([]byte, error) {
	// Build and execute all tasks in one Run call
	tasks := chromedp.Tasks{
		emulation.SetDeviceMetricsOverride(
			*arguments.windowWidth,
			*arguments.windowHeight,
			deviceScaleFactor,
			false,
		),
		// Use page.Navigate directly to avoid hanging on pages
		// that never fire the load event within a constrained viewport.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, _, _, err := page.Navigate(url).Do(ctx)
			return err
		}),
		// Wait for navigation to actually commit before starting the user-specified delay.
		// Raw page.Navigate returns immediately; without this check, parallel tabs
		// may still be on about:blank when the screenshot fires.
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 100; i++ {
				var readyState string
				if err := chromedp.Evaluate(`document.readyState`, &readyState).Do(ctx); err != nil {
					return err
				}
				if readyState == "interactive" || readyState == "complete" {
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return nil
		}),
		chromedp.Sleep(time.Duration(*arguments.waitSeconds) * time.Second),
	}

	var buf []byte
	switch {
	case *arguments.fullScreenshot:
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			data, err := captureFullPage(ctx)
			if err != nil {
				return err
			}
			buf = data
			return nil
		}))
	case *arguments.querySelector != "":
		qs := *arguments.querySelector
		tasks = append(tasks,
			chromedp.WaitVisible(qs, chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				data, err := captureElement(ctx, qs)
				if err != nil {
					return err
				}
				buf = data
				return nil
			}),
		)
	default:
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			data, err := captureViewport(ctx, nil)
			if err != nil {
				return err
			}
			buf = data
			return nil
		}))
	}

	if err := chromedp.Run(ctx, tasks); err != nil {
		return nil, err
	}
	if *arguments.showAddressBar {
		combined, err := addAddressBar(ctx, url, buf)
		if err != nil {
			return nil, err
		}
		buf = combined
	}
	return buf, nil
}

// captureFullPage resizes the viewport to the full page dimensions and takes
// a normal viewport screenshot. This avoids captureBeyondViewport which is
// unreliable when multiple tabs capture concurrently.
func captureFullPage(ctx context.Context) ([]byte, error) {
	var dims []int64
	if err := chromedp.Evaluate(
		`[document.documentElement.scrollWidth, Math.max(document.documentElement.scrollHeight, document.body.scrollHeight)]`,
		&dims,
	).Do(ctx); err != nil {
		return nil, err
	}
	w := max(*arguments.windowWidth, dims[0])
	h := dims[1]
	if err := emulation.SetDeviceMetricsOverride(
		clampDim(w), clampDim(h), deviceScaleFactor, false,
	).Do(ctx); err != nil {
		return nil, err
	}
	return captureViewport(ctx, nil)
}

// captureElement expands the viewport to contain the target element, then
// captures it with a clip rect. This avoids captureBeyondViewport which is
// unreliable when multiple tabs capture concurrently.
// Caller must ensure the selector is already present/visible (e.g. via WaitVisible).
func captureElement(ctx context.Context, selector string) ([]byte, error) {
	x, y, w, h, err := getElementRect(ctx, selector)
	if err != nil {
		return nil, err
	}
	// Expand viewport so the element is fully visible
	needW := max(*arguments.windowWidth, int64(math.Ceil(x+w)))
	needH := max(*arguments.windowHeight, int64(math.Ceil(y+h)))
	if err := emulation.SetDeviceMetricsOverride(
		clampDim(needW), clampDim(needH), deviceScaleFactor, false,
	).Do(ctx); err != nil {
		return nil, err
	}
	// Re-read rect after viewport resize (layout may shift)
	x, y, w, h, err = getElementRect(ctx, selector)
	if err != nil {
		return nil, err
	}
	rx, ry := math.Round(x), math.Round(y)
	return captureViewport(ctx, &page.Viewport{
		X: rx, Y: ry,
		Width:  math.Round(w + x - rx),
		Height: math.Round(h + y - ry),
		Scale:  1,
	})
}

// captureViewport takes a PNG screenshot of the current viewport.
// If clip is non-nil, only the specified region is captured.
func captureViewport(ctx context.Context, clip *page.Viewport) ([]byte, error) {
	action := page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng)
	if clip != nil {
		action = action.WithClip(clip)
	}
	return action.Do(ctx)
}

// getElementRect returns the bounding client rect of the first element
// matching the given CSS selector.
func getElementRect(ctx context.Context, selector string) (x, y, w, h float64, err error) {
	var rect []float64
	if err = chromedp.Evaluate(
		`(function(){var r=document.querySelector(`+"`"+selector+"`"+`).getBoundingClientRect();return[r.x,r.y,r.width,r.height]})()`,
		&rect,
	).Do(ctx); err != nil {
		return
	}
	return rect[0], rect[1], rect[2], rect[3], nil
}

// addAddressBar renders a browser-style address bar with favicon and URL using
// chromedp, then stitches it on top of the page screenshot.
func addAddressBar(ctx context.Context, pageURL string, pageBuf []byte) ([]byte, error) {
	// Get favicon as a data URI from current page (still on the target page).
	// Collects all favicon candidates from the page, tries data: URIs first
	// (no fetch needed), then attempts each URL-based candidate with fetch,
	// falling back to /favicon.ico as a last resort.
	//
	// Covered patterns:
	//   <link rel="icon" href="...">              — standard
	//   <link rel="shortcut icon" href="...">     — legacy
	//   <link rel="apple-touch-icon" href="...">  — iOS
	//   <link rel="icon" href="data:image/...">   — embedded data URI
	//   /favicon.ico                              — convention fallback
	var faviconDataURL string
	if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`
(async function() {
  // Collect all favicon candidates from <link> tags, then /favicon.ico
  var cs = [];
  document.querySelectorAll('link[rel*="icon"]').forEach(function(el) {
    if (el.href) cs.push(el.href);
  });
  cs.push(location.origin + '/favicon.ico');

  // Deduplicate while preserving order
  var seen = {};
  cs = cs.filter(function(u) {
    if (seen[u]) return false;
    seen[u] = true;
    return true;
  });

  // stripDarkMode removes @media (prefers-color-scheme: dark) blocks from SVG
  // to prevent favicons from rendering as white-on-white in the address bar.
  function stripDarkMode(svgText) {
    return svgText.replace(/@media\s*\(\s*prefers-color-scheme:\s*dark\s*\)\s*\{[^}]*\{[^}]*\}\s*\}/g, '');
  }

  // Try data: URIs first (instant, no fetch needed)
  for (var i = 0; i < cs.length; i++) {
    if (!cs[i].startsWith('data:')) continue;
    if (cs[i].indexOf('image/svg') !== -1) {
      try {
        var svgContent;
        if (cs[i].indexOf(';base64,') !== -1) {
          svgContent = atob(cs[i].split(';base64,')[1]);
        } else {
          svgContent = decodeURIComponent(cs[i].split(',').slice(1).join(','));
        }
        return 'data:image/svg+xml;base64,' + btoa(unescape(encodeURIComponent(stripDarkMode(svgContent))));
      } catch(e) { return cs[i]; }
    }
    return cs[i];
  }

  // Try each URL-based candidate
  // If fetch succeeds: convert to data URL (SVG: strip dark mode first)
  // If fetch fails (CORS etc.): use the URL directly in <img> tag
  for (var i = 0; i < cs.length; i++) {
    if (cs[i].startsWith('data:')) continue;
    try {
      var r = await fetch(cs[i]);
      if (!r.ok) continue;
      var b = await r.blob();
      if (b.size === 0) continue;
      if (b.type === 'image/svg+xml' || cs[i].endsWith('.svg')) {
        var svgText = await b.text();
        return 'data:image/svg+xml;base64,' + btoa(unescape(encodeURIComponent(stripDarkMode(svgText))));
      }
      var result = await new Promise(function(ok) {
        var rd = new FileReader();
        rd.onload = function() { ok(rd.result); };
        rd.onerror = rd.onabort = function() { ok(''); };
        rd.readAsDataURL(b);
      });
      if (result) return result;
    } catch (e) {
      // CORS or network error: return the URL directly for <img> tag
      return cs[i];
    }
  }
  return '';
})()`,
		&faviconDataURL,
		func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
			return p.WithAwaitPromise(true)
		},
	)); err != nil {
		faviconDataURL = ""
	}

	// Decode page screenshot to get pixel dimensions
	pageImg, err := png.Decode(bytes.NewReader(pageBuf))
	if err != nil {
		return nil, fmt.Errorf("decode page screenshot: %w", err)
	}
	pageW := pageImg.Bounds().Dx()

	// Calculate CSS width to match the page screenshot pixel width
	cssW := int64(math.Round(float64(pageW) / deviceScaleFactor))
	const barCSSH int64 = 52

	// Build address bar HTML and capture it in the same tab
	barHTML := buildAddressBarHTML(pageURL, faviconDataURL)
	dataURL := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(barHTML))

	var barBuf []byte
	if err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(cssW, barCSSH, deviceScaleFactor, false),
		chromedp.Navigate(dataURL),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			data, err := captureViewport(ctx, nil)
			if err != nil {
				return err
			}
			barBuf = data
			return nil
		}),
	); err != nil {
		return nil, fmt.Errorf("capture address bar: %w", err)
	}

	// Decode bar screenshot
	barImg, err := png.Decode(bytes.NewReader(barBuf))
	if err != nil {
		return nil, fmt.Errorf("decode bar screenshot: %w", err)
	}

	// Stitch: bar on top, page below
	barB := barImg.Bounds()
	pageB := pageImg.Bounds()
	result := image.NewRGBA(image.Rect(0, 0, pageB.Dx(), barB.Dy()+pageB.Dy()))
	draw.Draw(result, image.Rect(0, 0, barB.Dx(), barB.Dy()), barImg, barB.Min, draw.Src)
	draw.Draw(result, image.Rect(0, barB.Dy(), pageB.Dx(), barB.Dy()+pageB.Dy()), pageImg, pageB.Min, draw.Src)

	var out bytes.Buffer
	if err := png.Encode(&out, result); err != nil {
		return nil, fmt.Errorf("encode combined screenshot: %w", err)
	}
	return out.Bytes(), nil
}

// buildAddressBarHTML returns an HTML page that renders a browser-style address bar.
func buildAddressBarHTML(pageURL, faviconURL string) string {
	escapedURL := html.EscapeString(pageURL)
	imgTag := ""
	if faviconURL != "" {
		imgTag = fmt.Sprintf(`<img src="%s" width="16" height="16" style="margin-right:8px;flex-shrink:0" onerror="this.style.display='none'">`, html.EscapeString(faviconURL))
	}
	return fmt.Sprintf(`<!DOCTYPE html><html><head><style>*{margin:0;padding:0;box-sizing:border-box}`+
		`body{background:#dee1e6;display:flex;align-items:center;height:52px;padding:0 8px}`+
		`.bar{display:flex;align-items:center;flex:1;height:36px;padding:0 12px;background:#fff;border-radius:24px;`+
		`font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;font-size:14px;overflow:hidden}`+
		`.url{color:#202124;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}`+
		`</style></head><body><div class="bar">%s<span class="url">%s</span></div></body></html>`, imgTag, escapedURL)
}

// clampDim clamps a CSS dimension to Chrome's max texture size to avoid tiling artifacts.
// The GPU limit is in physical pixels, so we divide by deviceScaleFactor.
func clampDim(v int64) int64 {
	maxCSS := int64(math.Floor(float64(maxPhysicalDim) / deviceScaleFactor))
	return min(v, maxCSS)
}

// closeAllTargets closes all page targets via CDP so no tabs remain in the
// session history. This prevents leftover tabs when reusing a profile with -r.
func closeAllTargets(ctx context.Context) {
	targets, err := chromedp.Targets(ctx)
	if err != nil {
		return
	}
	c := chromedp.FromContext(ctx)
	if c == nil || c.Browser == nil {
		return
	}
	for _, t := range targets {
		if t.Type == "page" {
			target.CloseTarget(t.TargetID).Do(cdp.WithExecutor(ctx, c.Browser))
		}
	}
}

// removeStaleChromeLocks removes lock files left by previous crashed runs.
func removeStaleChromeLocks(userDataDir string) {
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		p := filepath.Join(userDataDir, name)
		if err := os.Remove(p); err == nil {
			log.Printf("removed stale lock: %s", p)
		}
	}
}

// cleanupProfileCache deletes the cached profile directory unless -s is specified.
func cleanupProfileCache(cacheDir string) {
	if cacheDir == "" || *arguments.reUseProfile {
		return
	}
	log.Printf("delete cached profile: %s", cacheDir)
	if err := os.RemoveAll(cacheDir); err != nil {
		log.Printf("failed to delete cached profile: %v", err)
	}
}

// chromeProfileCacheRoot returns the root directory for cached Chrome profiles.
// Can be overridden by the SITESNAP_CACHE_DIR environment variable.
func chromeProfileCacheRoot() string {
	if dir := os.Getenv("SITESNAP_CACHE_DIR"); dir != "" {
		return dir
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to get user home directory: %v", err)
	}
	return filepath.Join(homeDir, ".sitesnap")
}

func logSettings(profileCacheDir string) {
	for i, u := range urls {
		log.Printf("         url[%d]: %s", i, u)
	}
	log.Printf("          query: %s", *arguments.querySelector)
	log.Printf("         output: %s", *arguments.outputPath)
	log.Printf("    profile dir: %s", *arguments.profileDir)
	log.Printf("       viewport: %dx%d", *arguments.windowWidth, *arguments.windowHeight)
	log.Printf("   scale factor: %.1f", deviceScaleFactor)
	log.Printf("full screenshot: %v", *arguments.fullScreenshot)
	log.Printf("       headless: %v", !*arguments.noHeadless)
	log.Printf("       parallel: %d", *arguments.parallel)
	log.Printf("   wait seconds: %d", *arguments.waitSeconds)
	for i, cf := range chromeFlags {
		log.Printf("  chrome flag[%d]: %s", i, cf)
	}
	if profileCacheDir != "" {
		log.Printf("  profile cache: %s", profileCacheDir)
		log.Printf("   save profile: %v", *arguments.reUseProfile)
	}
}

// Common function

func customUsage(description string) func() {
	optionFieldWidth := "16" // Recommended width = general: 16, bool only: 5
	b := new(bytes.Buffer)
	func() { flag.CommandLine.SetOutput(b); flag.PrintDefaults(); flag.CommandLine.SetOutput(nil) }()
	return func() {
		re := regexp.MustCompile(`(?m)^ +(-\S+)(?: (\S+))?\n*(\s+)(.*)\n`)
		usages := strings.Split(re.ReplaceAllStringFunc(b.String(), func(m string) string {
			valueType := strings.ReplaceAll("<"+strings.TrimSpace(re.FindStringSubmatch(m)[2])+">", "<>", "")
			return fmt.Sprintf("  %-"+optionFieldWidth+"s %s\n", re.FindStringSubmatch(m)[1]+" "+valueType, re.FindStringSubmatch(m)[4])
		}), "\n")
		sort.SliceStable(usages, func(i, j int) bool { return strings.Contains(usages[i], Req) && !strings.Contains(usages[j], Req) })
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [OPTIONS]\n  version: %s\n\n", func() string { e, _ := os.Executable(); return filepath.Base(e) }(), version)
		fmt.Fprintf(flag.CommandLine.Output(), "Description:\n  %s\n\n", description)
		fmt.Fprintf(flag.CommandLine.Output(), "Options:\n%s", strings.Join(usages, "\n"))
	}
}
