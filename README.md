# chromedp-screenshots

A web page screenshot tool with parallel multi-URL capture and lock-free Chrome profile support, powered by [chromedp](https://github.com/chromedp/chromedp) (headless Chrome). Only Chrome is required — no Puppeteer, no Playwright, no Node.js, no Python.

### Why chromedp-screenshots?

- **Parallel capture** – Multiple URLs are captured simultaneously in separate tabs within a single Chrome process. No sequential waiting — all pages load and render at the same time.
- **Lock-free profile usage** – When using a Chrome profile (`-p`), the tool copies it to an isolated cache directory. This means you can take screenshots with your logged-in session **even while your main browser is running** — no profile lock conflicts.

### Other Features

- **Viewport / Element / Full-page screenshot** – capture the visible area, a specific CSS selector (`-q`), or the entire scrollable page (`-f`)
- **Custom Chrome flags** – pass arbitrary Chrome flags with `-c`
- **Idempotent execution** – without `-r`, the cached profile is always freshly copied, ensuring consistent results regardless of previous runs

## Requirements

- Go 1.23+
- Google Chrome / Chromium installed

## Installation

```bash
go install github.com/xshoji/chromedp-screenshots@latest
```

Or build from source:

```bash
git clone https://github.com/xshoji/chromedp-screenshots.git
cd chromedp-screenshots
go build -o chromedp-screenshots .
```

## Usage

```bash
go run main.go -u <URL> -o /tmp/screenshot.png [options]
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-u` | *(required)* | URL to capture (can be specified multiple times) |
| `-o` | *(required)* | Output file path (auto-numbered with multiple URLs: `_001.png`, `_002.png`, …) |
| `-q` | `""` | CSS selector – screenshot the first matching element |
| `-p` | `""` | Chrome profile directory to copy and cache |
| `-w` | `3` | Wait seconds after navigation before capturing |
| `-wi` | `1280` | Viewport width |
| `-he` | `860` | Viewport height |
| `-s` | `2.0` | Device scale factor (2.0 = Retina) |
| `-f` | `false` | Enable full-page screenshot |
| `-d` | `false` | Enable debug mode |
| `-n` | `false` | Disable headless mode (show browser window) |
| `-r` | `false` | Reuse cached profile (do not delete after execution) |
| `-t` | `NumCPU` | Max number of parallel tabs for screenshot capture |
| `-c` | `""` | Extra Chrome flag as `key=value` (can be specified multiple times) |

### Examples

```bash
# Viewport screenshot
go run main.go -u="https://www.example.com/" -wi=1280 -he=800 -o=/tmp/example.png

# Element screenshot with CSS selector
go run main.go -u="https://news.yahoo.co.jp/" -q="#liveStream" -o="/tmp/livestream.png"

# Full-page screenshot
go run main.go -u="https://www.example.com/" -f -o=/tmp/fullpage.png

# Multiple URLs (parallel capture)
go run main.go -u="https://www.yahoo.co.jp/" -u="https://www.google.com/" -o=/tmp/sites.png

# With Chrome profile (for logged-in sessions)
go run main.go -u="https://example.com/dashboard" \
  -p="/Users/you/Library/Application Support/Google/Chrome/Default" \
  -r -o=/tmp/dashboard.png

# Custom Chrome flags
go run main.go -u="https://example.com/" -c "lang=ja" -c "disable-extensions"
```

### Details of the -p flag and the Google Chrome profile directory

> Chromium Docs - User Data Directory  
> https://chromium.googlesource.com/chromium/src/+/HEAD/docs/user_data_dir.md  

- The `-p` flag specifies a Chrome profile directory to copy and use for the screenshot session. This allows you to capture pages with your logged-in session without locking your main browser.
- The profile is copied to an isolated cache directory (`~/.chromedpscreenshots/`), so the original profile is never modified.
- Without `-r`, the cached copy is deleted after each run for idempotency. With `-r`, it is kept for reuse.


### Environment Variables

| Variable | Description |
|----------|-------------|
| `CHROMEDP_SCREENSHOTS_CACHE_DIR` | Override the default profile cache directory (`~/.chromedpscreenshots`) |

## Development

### Build

```bash
go build -ldflags="-s -w" -trimpath -o cdpss main.go
```

### Test

```bash
# Unit tests only (no Chrome required)
go test -v

# All tests including E2E (Chrome required)
CHROMEDP_SCREENSHOTS_E2E=1 go test -v
```

## Release

The release flow for this repository is automated with GitHub Actions.
Pushing Git tags triggers the release job.

```
# Release
git tag v0.0.1 && git push --tags


# Delete tag
v="v0.0.1"; git tag -d "${v}" && git push origin :"${v}"

# Delete tag and recreate new tag and push
v="v0.0.1"; git tag -d "${v}" && git push origin :"${v}"; git tag "${v}" -m "Release "; git push --tags
```

## License

See [LICENSE](LICENSE) for details.
