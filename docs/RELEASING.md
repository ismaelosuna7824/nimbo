# Releasing Nimbo

Releases are automatic. Every push (or merged pull request) to `main` runs
[`.github/workflows/release.yml`](../.github/workflows/release.yml), which
decides from the commit messages whether there is something to release. Nobody
pushes tags by hand.

## Commit messages decide the version

Commits follow [Conventional Commits](https://www.conventionalcommits.org):
`type(scope): description`. Since the last `vX.Y.Z` tag:

| Commits | Next version |
|---|---|
| any `type!:` or a `BREAKING CHANGE:` footer | major (`1.4.2` → `2.0.0`) |
| any `feat` | minor (`1.4.2` → `1.5.0`) |
| any `fix` or `perf` | patch (`1.4.2` → `1.4.3`) |
| only `docs`, `ci`, `chore`, `test`, `refactor`, `build`, `style`, … | no release |

The first release (no tags yet) is `v0.1.0`. With squash merges, the pull
request title becomes the commit message, so write PR titles the same way.
Messages that aren't Conventional Commits are ignored.

The logic is [`scripts/release/next-version.ts`](../scripts/release/next-version.ts)
(tests next to it, run in CI). To preview locally what `main` would release:

```sh
bun scripts/release/next-version.ts --repo ismaelosuna7824/nimbo --notes /tmp/changelog.md
cat /tmp/changelog.md
```

## What a release run does

1. **Version**: computes the version and the grouped changelog (Breaking
   changes, Features, Bug fixes, Performance). Nothing to release → the run
   stops here.
2. **Build** (in parallel): macOS universal (`.dmg`, `.app.tar.gz`), Windows x64
   (NSIS `-setup.exe`, `.msi`), Linux x86_64 and arm64 (`.AppImage`, `.deb`,
   `.rpm`). The version is written into `tauri.conf.json`, `package.json`,
   `Cargo.toml` and the `nimbo` sidecar at build time only; nothing is committed
   back. Each platform is smoke-tested (signatures, sidecar version, silent
   Windows install, package contents).
3. **Publish**: writes `latest.json` (for in-app updates) and `SHA256SUMS.txt`,
   then creates the tag `vX.Y.Z` on the built commit and the GitHub release
   *Nimbo vX.Y.Z* with the changelog, install steps
   ([`.github/release-notes.md`](../.github/release-notes.md)) and all assets.

The tag is created only after every build has passed, so a failed build leaves
no tag behind; the next push to `main` releases those commits too. Runs are
serialized (one at a time per branch). Re-running a run whose commit is already
tagged reuses that version and refreshes the release instead of failing.

## Dry runs

*Actions → Release → Run workflow* (or
`gh workflow run release.yml --ref <branch> -f dry_run=true`) builds everything
with the version it *would* release and uploads the files as workflow artifacts
(`nimbo-*`, plus `release-notes` with the rendered notes, checksums and
`latest.json`). No tag, no release. Dry runs work on any branch; publishing only
happens from `main`.

## In-app updates and the signing key

The desktop app uses the Tauri updater. It reads
`https://github.com/ismaelosuna7824/nimbo/releases/latest/download/latest.json`
and only installs updates signed with the key whose public half is
`plugins.updater.pubkey` in `apps/desktop/src-tauri/tauri.conf.json`.

The release build signs the updater artifacts with the repository secrets
`TAURI_SIGNING_PRIVATE_KEY` and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`
(`createUpdaterArtifacts` is enabled only in the workflow, so local builds need
no key). **Keep a backup of the private key and its password**: if they are
lost, existing installs can't verify new versions and users have to reinstall
manually.

To rotate the key: `bunx tauri signer generate -w nimbo-updater.key`, update the
two secrets (`gh secret set TAURI_SIGNING_PRIVATE_KEY < nimbo-updater.key`), put
the new public key in `tauri.conf.json` and release. Versions built before the
rotation can't update past it automatically.

`latest.json` maps `darwin-aarch64` and `darwin-x86_64` to the universal
`.app.tar.gz`, `windows-x86_64` to the NSIS installer and `linux-x86_64` /
`linux-aarch64` to the AppImages. Linux `.deb`/`.rpm` installs don't
self-update; the app links to the release page instead.
