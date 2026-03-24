# AGENTS.md — chromedp-screenshots

## Project Overview

chromedp-screenshots (`cds`) is a CLI tool that takes web page screenshots using headless Chrome via [chromedp](https://github.com/chromedp/chromedp). The entire tool is a single `main.go` with no external dependencies beyond chromedp. Only Chrome is required — no Node.js, no Puppeteer, no Playwright.

### Key Selling Points

- **Parallel multi-URL capture** — Multiple URLs are captured simultaneously in separate browser tabs within a single Chrome process, controlled by `-t` (defaults to `NumCPU`).
- **Lock-free Chrome profile usage** — The `-p` flag copies the user's Chrome profile to an isolated cache directory, allowing screenshots of logged-in sessions even while the main browser is running.
- **Single binary, zero runtime deps** — Just Go + Chrome. No npm, no pip, no webdriver.

## Architecture

All logic lives in `main.go`. No packages, no subdirectories.

### Main Flow (`main()`)

1. Parse flags
2. Copy Chrome profile to cache dir (if `-p` specified)
3. Launch a single Chrome process (`NewExecAllocator` → `NewContext`)
4. Spawn goroutines per URL, each creating a new tab (`NewContext(browserCtx)`)
5. Each tab: set viewport → navigate → wait → capture
6. Write PNG files
7. Shutdown Chrome, cleanup profile cache

### Screenshot Modes (the `switch` in `takeScreenshot`)

| Mode | Flag | Capture Method |
|------|------|----------------|
| **Viewport** | *(default)* | `page.CaptureScreenshot()` — captures the visible viewport |
| **Full-page** | `-f` | Viewport-resize approach (see below) |
| **Element** | `-q` | Viewport-resize + clip approach (see below) |

## Critical Design Decision: Why We Do NOT Use `chromedp.FullScreenshot`

**This is the most important architectural decision in this project.**

`chromedp.FullScreenshot` uses Chrome's `Page.captureScreenshot` with `captureBeyondViewport: true`. This has two serious problems:

### Problem 1: Parallel tab interference

Chrome's `captureBeyondViewport` compositor path is unreliable when multiple tabs capture concurrently in the same browser process. Screenshots come out with incorrect dimensions — sidebars get cropped, layouts break. This is a Chrome-level issue, not a chromedp bug.

### Problem 2: GPU texture tiling artifacts

When a page's full height exceeds Chrome's GPU texture limit (~16384 CSS pixels), the compositor produces **tiled/repeated artifacts** — the same content is rendered in a grid pattern across the entire image.

### Our Solution: Viewport-resize approach

Instead of `captureBeyondViewport`, we:

1. Navigate to the page with the user-specified viewport
2. Query the full page dimensions via JavaScript (`scrollWidth`, `scrollHeight`)
3. Call `emulation.SetDeviceMetricsOverride` to resize the viewport to match the full content (clamped to `maxViewportDim = 16384`)
4. Take a normal `page.CaptureScreenshot()` (no `captureBeyondViewport`)

This is safe for parallel execution because `SetDeviceMetricsOverride` is scoped to each tab's CDP session.

The same approach is applied to `-q` (element screenshots): we expand the viewport to contain the target element, then capture with a `clip` rect.

## Constants

- `maxPhysicalDim = 16384` — Chrome's GPU texture limit in **physical pixels**. The effective CSS pixel limit is `floor(16384 / deviceScaleFactor)` (e.g., 8192 CSS px at scale 2.0). Both `-f` and `-q` modes clamp to this derived value.

## Testing

```bash
# Unit tests (no Chrome required)
go test -v

# E2E tests (Chrome required)
CHROMEDP_SCREENSHOTS_E2E=1 go test -v
```

- Unit tests cover: `outputPath`, `stringSlice`, `chromeProfileCacheRoot`, `removeStaleChromeLocks`, `setupProfileCache`, `cleanupProfileCache`
- E2E tests (`TestE2E_*`) require `CHROMEDP_SCREENSHOTS_E2E=1` and a running Chrome installation

## Build

```bash
go build -ldflags="-s -w" -trimpath -o cds main.go
```

## Conventions

- Single-file project: all code in `main.go`, tests in `main_test.go`
- Package-level `var arguments` struct holds all flag pointers
- `stringSlice` type enables repeated flags (`-u`, `-c`)
- Profile cache lives under `~/.chromedpscreenshots/` (overridable via `CHROMEDP_SCREENSHOTS_CACHE_DIR`)
- Output numbering for multiple URLs: `<base>_001.png`, `_002.png`, ...
