Design, deploy and load-test cloud infrastructure on your laptop. Full install guide: [docs/INSTALL.md](https://github.com/{{REPO}}/blob/{{TAG}}/docs/INSTALL.md).

## What's changed

{{CHANGELOG}}

Already have Nimbo? It offers this update on its next start (or **Settings → About → Check for updates**). Linux `.deb`/`.rpm` installs: download the new package below.

## Download

| OS | File |
|---|---|
| macOS 12+ (Apple Silicon and Intel) | [Nimbo-macOS-universal.dmg](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-macOS-universal.dmg) |
| Windows 10/11 x64 | [Nimbo-Windows-x64-setup.exe](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Windows-x64-setup.exe) (or the `.msi`) |
| Linux x86_64 | [AppImage](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Linux-x86_64.AppImage) · [.deb](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Linux-x86_64.deb) · [.rpm](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Linux-x86_64.rpm) |
| Linux arm64 | [AppImage](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Linux-aarch64.AppImage) · [.deb](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Linux-aarch64.deb) · [.rpm](https://github.com/{{REPO}}/releases/download/{{TAG}}/Nimbo-Linux-aarch64.rpm) |

**Requirements:** a Docker engine (Docker Desktop, OrbStack, Colima, Rancher Desktop or Docker Engine on Linux) to run local sandboxes. Designing and exporting Terraform works without it; on first launch Nimbo checks Docker and explains anything missing.

## Install

The builds are not signed with a paid certificate, so your OS asks for confirmation the first time.

**macOS** — open the `.dmg` and drag Nimbo to Applications. The first launch is blocked ("Apple could not verify…"): open **System Settings → Privacy & Security**, scroll to *Nimbo was blocked* and click **Open Anyway** (on macOS 14 and earlier you can also right-click Nimbo → **Open**). Alternative, in Terminal:

```sh
xattr -dr com.apple.quarantine /Applications/Nimbo.app
```

**Windows** — run `Nimbo-Windows-x64-setup.exe`. If SmartScreen shows "Windows protected your PC", click **More info → Run anyway**. The installer fetches WebView2 if it is missing.

**Linux**

```sh
# AppImage (needs libfuse2: sudo apt install libfuse2, or run with --appimage-extract-and-run)
chmod +x Nimbo-Linux-*.AppImage && ./Nimbo-Linux-*.AppImage
# Debian / Ubuntu
sudo apt install ./Nimbo-Linux-*.deb
# Fedora / RHEL (openSUSE: sudo zypper install ./Nimbo-Linux-*.rpm)
sudo dnf install ./Nimbo-Linux-*.rpm
```

## Checksums (SHA-256)

```
{{CHECKSUMS}}
```

Verify with `shasum -a 256 -c SHA256SUMS.txt --ignore-missing` (macOS/Linux) or `Get-FileHash <file>` (PowerShell).
