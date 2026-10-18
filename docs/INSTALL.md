# Installing Nimbo

Download the file for your computer from the
[latest release](https://github.com/ismaelosuna7824/nimbo/releases/latest):

| OS | File |
|---|---|
| macOS 12 or newer (Apple Silicon and Intel) | `Nimbo-macOS-universal.dmg` |
| Windows 10/11 (x64) | `Nimbo-Windows-x64-setup.exe` (or `Nimbo-Windows-x64.msi`) |
| Linux x86_64 | `Nimbo-Linux-x86_64.AppImage`, `.deb` or `.rpm` |
| Linux arm64 | `Nimbo-Linux-aarch64.AppImage`, `.deb` or `.rpm` |

Nimbo's builds are free and not signed with a paid Apple or Microsoft
certificate, so the first launch needs one extra confirmation. The steps below
are the whole process. To build from source instead, see [SETUP.md](SETUP.md).

**Requirement:** a Docker engine (Docker Desktop, OrbStack, Colima, Rancher
Desktop, or Docker Engine on Linux) to deploy into the local sandbox and load
test. Designing and importing/exporting Terraform work without it. On first
launch Nimbo checks Docker and explains anything missing.

## macOS

1. Open `Nimbo-macOS-universal.dmg` and drag **Nimbo** to **Applications**.
2. Open Nimbo. macOS blocks it: *"Apple could not verify 'Nimbo' is free of
   malware…"*. Click **Done** (not *Move to Trash*).
3. Open **System Settings → Privacy & Security**, scroll down to
   *"Nimbo" was blocked to protect your Mac* and click **Open Anyway**, then
   confirm with your password.
4. Nimbo opens; later launches need nothing extra.

On macOS 14 (Sonoma) and earlier, right-clicking Nimbo in Applications →
**Open** → **Open** also works.

**Terminal alternative** (removes the download quarantine flag):

```sh
xattr -dr com.apple.quarantine /Applications/Nimbo.app
```

`Nimbo-macOS-universal.app.tar.gz` contains the same app without the disk
image: `tar -xzf Nimbo-macOS-universal.app.tar.gz` and move `Nimbo.app` to
Applications.

## Windows

1. Run `Nimbo-Windows-x64-setup.exe`.
2. If SmartScreen shows *"Windows protected your PC"*, click **More info →
   Run anyway**.
3. Follow the installer. It installs for your user only (no administrator
   needed) and downloads Microsoft Edge WebView2 if it is missing.

The `.msi` is for managed or all-users installs
(`msiexec /i Nimbo-Windows-x64.msi`). Some browsers mark unsigned downloads as
"not commonly downloaded": choose **Keep**.

## Linux

**AppImage** (any distribution):

```sh
chmod +x Nimbo-Linux-x86_64.AppImage
./Nimbo-Linux-x86_64.AppImage
```

AppImages need FUSE 2. If it doesn't start (*"dlopen(): error loading
libfuse.so.2"*), install it — `sudo apt install libfuse2` (Ubuntu 24.04:
`libfuse2t64`), `sudo dnf install fuse-libs` — or run without FUSE:
`./Nimbo-Linux-x86_64.AppImage --appimage-extract-and-run`.

**Debian / Ubuntu** (pulls in WebKitGTK automatically):

```sh
sudo apt install ./Nimbo-Linux-x86_64.deb
```

**Fedora / RHEL**:

```sh
sudo dnf install ./Nimbo-Linux-x86_64.rpm
```

openSUSE: `sudo zypper install ./Nimbo-Linux-x86_64.rpm`. On arm64, use the
`aarch64` files.

## Verify the download (optional)

Each release has a `SHA256SUMS.txt`:

```sh
shasum -a 256 -c SHA256SUMS.txt --ignore-missing   # macOS / Linux
```

```powershell
Get-FileHash .\Nimbo-Windows-x64-setup.exe   # compare with SHA256SUMS.txt
```

## Troubleshooting

| Problem | Fix |
|---|---|
| macOS: *"Nimbo is damaged and can't be opened"* | The download was altered or the quarantine flag got stuck: run `xattr -dr com.apple.quarantine /Applications/Nimbo.app`, or download the `.dmg` again. |
| macOS: no *Open Anyway* button | It only appears for about an hour after the blocked launch: open Nimbo again, then go back to Privacy & Security. |
| Windows: no *Run anyway* button | Click **More info** first. On managed PCs, SmartScreen policy may forbid it: use the `.msi` through your IT or build from source. |
| Linux: blank window or WebKit errors | Install WebKitGTK 4.1: `sudo apt install libwebkit2gtk-4.1-0` / `sudo dnf install webkit2gtk4.1`. With NVIDIA drivers try `WEBKIT_DISABLE_DMABUF_RENDERER=1 ./Nimbo-Linux-x86_64.AppImage`. |
| *"the nimbo engine did not start"* | The bundled engine was blocked. macOS: run the `xattr` command above. Windows: allow `nimbo.exe` in your antivirus. Then reopen Nimbo. |
| Docker is missing or not running | Install/start Docker; the first-run guide in the app re-checks it. `nimbo doctor` (see [SETUP.md](SETUP.md)) shows the same checks. |

## Uninstall

- **macOS**: drag `/Applications/Nimbo.app` to the Trash.
- **Windows**: *Settings → Apps → Installed apps → Nimbo → Uninstall*.
- **Linux**: `sudo apt remove nimbo` / `sudo dnf remove nimbo`, or delete the AppImage.

Your workflows and settings stay in `~/.nimbo` (Windows:
`%USERPROFILE%\.nimbo`); delete that folder to remove them too.
