export const GITHUB_URL = "https://github.com/ismaelosuna7824/nimbo";
export const RELEASES_URL = `${GITHUB_URL}/releases/latest`;

/** Stable asset names published by .github/workflows/release.yml on every release. */
const latestAsset = (name: string) => `${GITHUB_URL}/releases/latest/download/${name}`;
export const DOWNLOADS = {
  mac: latestAsset("Nimbo-macOS-universal.dmg"),
  windows: latestAsset("Nimbo-Windows-x64-setup.exe"),
  linux: latestAsset("Nimbo-Linux-x86_64.AppImage"),
  linuxArm: latestAsset("Nimbo-Linux-aarch64.AppImage"),
} as const;

export const DOCS = {
  setup: `${GITHUB_URL}/blob/main/docs/SETUP.md`,
  mcp: `${GITHUB_URL}/blob/main/docs/MCP.md`,
  architecture: `${GITHUB_URL}/blob/main/docs/ARCHITECTURE.md`,
  install: `${GITHUB_URL}/blob/main/docs/INSTALL.md`,
  learn: `${GITHUB_URL}/blob/main/docs/CONTRIBUTING-learn.md`,
  readme: `${GITHUB_URL}#readme`,
} as const;
export const FLOCI_URL = "https://floci.io";

export const SETUP_COMMAND_UNIX = `git clone ${GITHUB_URL}.git && cd nimbo && ./setup.sh`;
export const SETUP_COMMAND_WINDOWS = `git clone ${GITHUB_URL}.git; cd nimbo; powershell -ExecutionPolicy Bypass -File .\\setup.ps1`;
