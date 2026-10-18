export const GITHUB_URL = "https://github.com/ismaelosuna7824/nimbo";
export const RELEASES_URL = `${GITHUB_URL}/releases/latest`;
export const DOCS = {
  setup: `${GITHUB_URL}/blob/main/docs/SETUP.md`,
  mcp: `${GITHUB_URL}/blob/main/docs/MCP.md`,
  architecture: `${GITHUB_URL}/blob/main/docs/ARCHITECTURE.md`,
  readme: `${GITHUB_URL}#readme`,
} as const;
export const FLOCI_URL = "https://floci.io";

export const SETUP_COMMAND_UNIX = `git clone ${GITHUB_URL}.git && cd nimbo && ./setup.sh`;
export const SETUP_COMMAND_WINDOWS = `git clone ${GITHUB_URL}.git; cd nimbo; powershell -ExecutionPolicy Bypass -File .\\setup.ps1`;
