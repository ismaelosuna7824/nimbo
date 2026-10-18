import type { ReactNode } from "react"
import type { SetupFix, SetupStatus } from "@/lib/api/types"

export type Platform = "darwin" | "windows" | "linux"

export const PLATFORM_IDS: Platform[] = ["darwin", "windows", "linux"]

/** Troubleshooting entry of the Start Docker step. */
interface Tip {
  id: string
  question: string
  answer: ReactNode
}

/**
 * Everything the wizard says that depends on the operating system. On this
 * machine the daemon's fixes (already tailored to OS, distro and arch) are
 * shown; `installFixes` / `startFixes` / `wslFixes` / `permissionFixes` are the
 * generic instructions used when previewing another OS.
 */
interface PlatformInfo {
  label: string
  /** Where the engine's icon shows up once it runs. */
  runningSign: string
  install: { title: string; text: ReactNode }
  installFixes: SetupFix[]
  /** The daemon can start the engine itself (POST /setup/actions/start-docker). */
  canStart: boolean
  startText: ReactNode
  startFixes: SetupFix[]
  tips: Tip[]
  wslFixes?: SetupFix[]
  permissionFixes?: SetupFix[]
}

const code = (s: string) => <code className="font-mono text-[0.92em]">{s}</code>

export const PLATFORMS: Record<Platform, PlatformInfo> = {
  darwin: {
    label: "macOS",
    runningSign: "a whale (Docker Desktop) or the OrbStack icon appears in the menu bar at the top of your screen",
    install: {
      title: "On a Mac: Docker Desktop or OrbStack",
      text: (
        <>
          <b className="font-medium text-foreground">Docker Desktop</b> is the most common choice. <b className="font-medium text-foreground">OrbStack</b>{" "}
          is a lighter, faster alternative made for Macs. Both are free for personal use — install one, not both. Pick the download that matches your Mac's
          chip (Apple silicon or Intel).
        </>
      ),
    },
    installFixes: [
      { kind: "link", label: "Get Docker Desktop for Mac", url: "https://docs.docker.com/desktop/setup/install/mac-install/" },
      { kind: "link", label: "Get OrbStack", url: "https://orbstack.dev/download" },
      { kind: "command", label: "Or, with Homebrew", command: "brew install --cask orbstack" },
    ],
    canStart: true,
    startText: <>Open Docker Desktop or OrbStack from your Applications folder — or press the button and Nimbo does it for you.</>,
    startFixes: [
      { kind: "command", label: "Or from Terminal", command: "open -a Docker   # or: open -a OrbStack" },
    ],
    tips: [
      {
        id: "slow",
        question: "It's been starting for a long time",
        answer: <>Click the whale in the menu bar and look for a message: the first start asks you to accept terms and may ask for your password. If it's stuck, choose Quit and start it again.</>,
      },
      {
        id: "other",
        question: "I use Colima or Rancher Desktop",
        answer: <>Those work too. Start them as usual (for example {code("colima start")}); this page turns green as soon as Docker answers.</>,
      },
      {
        id: "login",
        question: "Start Docker automatically next time",
        answer: <>Docker Desktop: Settings › General › “Start Docker Desktop when you sign in”. OrbStack: Settings › “Start at login”.</>,
      },
    ],
  },
  windows: {
    label: "Windows",
    runningSign: "a whale appears in the system tray, next to the clock",
    install: {
      title: "On Windows: Docker Desktop",
      text: <>Download and run the installer and keep “Use WSL 2” checked. It may ask you to restart your computer — say yes, then open Docker Desktop once.</>,
    },
    installFixes: [
      { kind: "link", label: "Get Docker Desktop for Windows", url: "https://docs.docker.com/desktop/setup/install/windows-install/" },
      { kind: "command", label: "Or, in PowerShell", command: "winget install -e --id Docker.DockerDesktop" },
    ],
    canStart: true,
    startText: <>Open Docker Desktop from the Start menu — or press the button and Nimbo does it for you.</>,
    startFixes: [
      { kind: "command", label: "Or from PowerShell", command: '& "C:\\Program Files\\Docker\\Docker\\Docker Desktop.exe"' },
    ],
    tips: [
      {
        id: "slow",
        question: "It's been starting for a long time",
        answer: <>Click the whale in the system tray and look for a message: the first start asks you to accept terms. If it says WSL needs an update, run {code("wsl --update")} in PowerShell.</>,
      },
      {
        id: "virtualization",
        question: "“Virtualization is not enabled”",
        answer: <>Docker needs virtualization turned on in your computer's firmware (BIOS/UEFI), usually called “Intel VT-x” or “AMD-V / SVM”. Your PC maker's support page shows how.</>,
      },
      {
        id: "login",
        question: "Start Docker automatically next time",
        answer: <>Docker Desktop: Settings › General › “Start Docker Desktop when you sign in to your computer”.</>,
      },
    ],
    wslFixes: [
      { kind: "command", label: "In PowerShell, run as Administrator, then restart", command: "wsl --install" },
      { kind: "link", label: "Microsoft's WSL guide", url: "https://learn.microsoft.com/windows/wsl/install" },
    ],
  },
  linux: {
    label: "Linux",
    runningSign: "“systemctl status docker” in a terminal shows the service as active (running)",
    install: {
      title: "On Linux: Docker Engine",
      text: <>Install Docker Engine (the free command-line version) for your distribution, then turn on its background service.</>,
    },
    installFixes: [
      { kind: "link", label: "Docker Engine install guide", url: "https://docs.docker.com/engine/install/" },
      { kind: "command", label: "Docker's official install script", command: "curl -fsSL https://get.docker.com | sh" },
    ],
    canStart: false,
    startText: <>Docker Engine runs as a system service. Turn it on once — {code("enable")} also starts it at every boot:</>,
    startFixes: [{ kind: "command", label: "In a terminal", command: "sudo systemctl enable --now docker" }],
    tips: [
      {
        id: "status",
        question: "The service won't start",
        answer: <>Run {code("sudo systemctl status docker")} and {code("journalctl -u docker --no-pager | tail")} to see why; a reboot after installing often helps.</>,
      },
      {
        id: "other",
        question: "I use Podman or rootless Docker",
        answer: <>Those work if the Docker socket is enabled (for Podman: {code("systemctl --user enable --now podman.socket")}) and {code("DOCKER_HOST")} points at it.</>,
      },
    ],
    permissionFixes: [
      { kind: "command", label: "In a terminal, then log out and back in", command: "sudo usermod -aG docker $USER" },
      { kind: "link", label: "Docker's post-install guide", url: "https://docs.docker.com/engine/install/linux-postinstall/" },
    ],
  },
}

/** "Apple silicon", "Intel", "ARM64", "x64" — how people name their CPU on each OS. */
export function archLabel(platform: Platform, arch: string) {
  if (platform === "darwin") return arch === "arm64" ? "Apple silicon" : "Intel"
  if (arch === "arm64") return "ARM64"
  return arch === "amd64" ? "x64" : arch
}

/** Real platform reported by the daemon, falling back to Linux for anything unexpected. */
export function detectedPlatform(status: SetupStatus | undefined): Platform | undefined {
  if (!status) return undefined
  return (PLATFORM_IDS as string[]).includes(status.platform) ? (status.platform as Platform) : "linux"
}
