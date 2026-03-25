# chromedp-screenshots

A web page screenshot tool with parallel multi-URL capture and lock-free Chrome profile support, powered by [chromedp](https://github.com/chromedp/chromedp) (headless Chrome). Only Chrome is required — no Puppeteer, no Playwright, no Node.js, no Python.

### Why chromedp-screenshots?

- **Parallel capture** – Multiple URLs are captured simultaneously in separate tabs within a single Chrome process. No sequential waiting — all pages load and render at the same time.
- **Lock-free profile usage** – When using a Chrome profile (`-p`), the tool copies it to an isolated cache directory. This means you can take screenshots with your logged-in session **even while your main browser is running** — no profile lock conflicts.

### Other Features

- **Viewport / Element / Full-page screenshot** – capture the visible area, a specific CSS selector (`-q`), or the entire scrollable page (`-f`)
- **Browser-style address bar** – add a realistic address bar with favicon and URL to the top of screenshots (`-b`), perfect for documentation and presentations
    - <img width="50%" height="50%" alt="sample" src="https://github.com/user-attachments/assets/8e82bfaf-fd89-40e8-9c56-e8f40baef3ee"/>

- **Custom Chrome flags** – pass arbitrary Chrome flags with `-c`
- **Idempotent execution** – without `-r`, the cached profile is always freshly copied, ensuring consistent results regardless of previous runs

## Requirements

- Go 1.26+
- Google Chrome / Chromium installed

## Installation

### Homebrew

```bash
brew install xshoji/tap/cps
```


### Build from source

```bash
git clone https://github.com/xshoji/chromedp-screenshots.git
cd chromedp-screenshots
go build -ldflags="-s -w" -trimpath -o cps main.go
```

## Usage

```bash
cps -u <URL> -o /tmp/screenshot.png [options]
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
| `-b` | `false` | Add browser-style address bar (favicon + URL) to the top of screenshot |
| `-d` | `false` | Enable debug mode |
| `-n` | `false` | Disable headless mode (show browser window) |
| `-r` | `false` | Reuse cached profile (do not delete after execution) |
| `-t` | `NumCPU` | Max number of parallel tabs for screenshot capture |
| `-c` | `""` | Extra Chrome flag as `key=value` (can be specified multiple times) |

### Examples

```bash
# Viewport screenshot
cps -u="https://www.example.com/" -wi=1280 -he=800 -o=/tmp/example.png

# Element screenshot with CSS selector
cps -u="https://news.yahoo.co.jp/" -q="#liveStream" -o="/tmp/livestream.png"

# Full-page screenshot
cps -u="https://www.example.com/" -f -o=/tmp/fullpage.png

# Multiple URLs (parallel capture)
cps -u="https://www.yahoo.co.jp/" -u="https://www.google.com/" -o=/tmp/sites.png

# With Chrome profile (for logged-in sessions)
cps -u="https://example.com/dashboard" \
  -p="/Users/you/Library/Application Support/Google/Chrome/Default" \
  -r -o=/tmp/dashboard.png

# With browser-style address bar
cps -u="https://www.example.com/" -b -o=/tmp/with_bar.png

# Custom Chrome flags
cps -u="https://example.com/" -c "lang=ja" -c "disable-extensions"
```

### Details of the -p flag and the Google Chrome profile directory

> Chromium Docs - User Data Directory  
> https://chromium.googlesource.com/chromium/src/+/HEAD/docs/user_data_dir.md  

- The `-p` flag specifies a Chrome profile directory to copy and use for the screenshot session. This allows you to capture pages with your logged-in session without locking your main browser.
- The original profile is never modified — it is always copied to an isolated directory.
- **Without `-r`**: the profile is copied to a system temporary directory (e.g., `/tmp/chromedpscreenshots-userdata-*`) and automatically deleted after each run. Your home directory is never touched.
- **With `-r`**: the profile is copied to a persistent cache directory (`~/.chromedpscreenshots/`, overridable via `CHROMEDP_SCREENSHOTS_CACHE_DIR`) and kept for reuse across runs.


### Limitations

- **Full-page screenshot size limit** — Full-page (`-f`) and element (`-q`) screenshots are limited by Chrome's GPU texture size of 16384 physical pixels per axis. With the default scale factor (`-s 2.0`), this means pages taller or wider than **8192 CSS pixels** will be clipped. At `-s 1.0`, the limit is 16384 CSS pixels.

### Tips

- **Icon fonts showing as ✕ marks** — Web fonts (e.g., Font Awesome, Material Icons) may not finish loading within the default wait time. Try increasing `-w` (e.g., `-w 10`).
- **Custom DNS resolution** — Use `-c` with `host-resolver-rules` to override DNS resolution:
  ```bash
  cps -u="https://example.com/" \
    -c "host-resolver-rules=MAP example.com 127.0.0.1" \
    -o=/tmp/screenshot.png
  ```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `CHROMEDP_SCREENSHOTS_CACHE_DIR` | Override the default persistent profile cache directory used with `-r` (default: `~/.chromedpscreenshots`) |

## Development

### Build

```bash
go build -ldflags="-s -w" -trimpath -o cps main.go
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
v="v0.0.1"; git tag -d "${v}" && git push origin :"${v}"; git tag "${v}"; git push --tags
```

## License

See [LICENSE](LICENSE) for details.
