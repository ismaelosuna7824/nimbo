<#
.SYNOPSIS
  Nimbo setup for Windows: checks/installs prerequisites, installs dependencies
  and builds the nimbo backend and the desktop app. macOS/Linux: use setup.sh.

.DESCRIPTION
  Commands:
    install  (default) check/install prerequisites, install dependencies, build the
             backend and the desktop app, offer to install it, run `nimbo doctor`
    dev      check prerequisites, install dependencies, build the backend, then start
             the app in development mode (bun run tauri dev)
    build    install dependencies and build everything (assumes prerequisites)
    doctor   build the backend if needed and run `nimbo doctor` (add -Json for JSON)
    help     show this help

  Options:
    -Yes        answer "yes" to every question (unattended install)
    -NoInstall  never install or change anything outside this folder: missing
                prerequisites are only reported, and the offers to install the app
                and to put the nimbo CLI on PATH are skipped

  Run from a PowerShell window:
    powershell -ExecutionPolicy Bypass -File .\setup.ps1 [command] [-Yes] [-NoInstall]

  See docs/SETUP.md for details and troubleshooting.
#>
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '', Justification = 'Interactive installer: colored console output is its UI')]
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'dev', 'build', 'doctor', 'help')]
    [string]$Command = 'install',
    [switch]$Yes,
    [switch]$NoInstall,
    [switch]$Json
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# Keep this file ASCII-only: Windows PowerShell 5.1 reads BOM-less scripts as ANSI.

if ($Command -eq 'help') { Get-Help $PSCommandPath -Detailed; exit 0 }
if ($Yes -and $NoInstall) { [Console]::Error.WriteLine('setup.ps1: -Yes and -NoInstall contradict each other; pick one'); exit 2 }
if ($Json -and $Command -ne 'doctor') { [Console]::Error.WriteLine("setup.ps1: -Json only applies to '.\setup.ps1 doctor'"); exit 2 }
if ($PSVersionTable.PSEdition -eq 'Core' -and -not $IsWindows) {
    [Console]::Error.WriteLine('setup.ps1 is for Windows. On macOS and Linux run ./setup.sh')
    exit 2
}

$Root = $PSScriptRoot
$Backend = Join-Path $Root 'backend'
$Desktop = Join-Path $Root 'apps\desktop'
$TauriDir = Join-Path $Desktop 'src-tauri'
$NimboBin = Join-Path $Backend 'bin\nimbo.exe'
$MinGo = '1.26'
$MinBun = '1.4'
$MinRust = '1.90'
$cargoToml = Join-Path $TauriDir 'Cargo.toml'
if (Test-Path $cargoToml) {
    $m = Select-String -Path $cargoToml -Pattern '^rust-version\s*=\s*"([0-9.]+)"' | Select-Object -First 1
    if ($m) { $MinRust = $m.Matches[0].Groups[1].Value }
}
$TauriPrereqsUrl = 'https://v2.tauri.app/start/prerequisites/#windows'

$OK = [string][char]0x2713
$BAD = [string][char]0x2717
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch { $null = $_ }

# ------------------------------------------------------------------ output --
$Clock = [Diagnostics.Stopwatch]::StartNew()
$script:StepNo = 0
$script:StepTotal = 0
$script:StepName = ''
$script:StepClock = $null

function Format-Elapsed([TimeSpan]$t) {
    $s = [int][Math]::Floor($t.TotalSeconds)
    if ($s -ge 60) { return ('{0}m {1:00}s' -f [int][Math]::Floor($s / 60), ($s % 60)) }
    return ('{0}s' -f $s)
}
function Write-Info([string]$msg) { Write-Host "  $msg" }
function Write-Note([string]$msg) { Write-Host "  $msg" -ForegroundColor DarkGray }
function Write-Warn([string]$msg) { Write-Host '  ! ' -ForegroundColor Yellow -NoNewline; Write-Host $msg }
function Write-RowOk([string]$name, [string]$detail) {
    Write-Host "  $OK " -ForegroundColor Green -NoNewline
    Write-Host ('{0,-28} ' -f $name) -NoNewline
    Write-Host $detail -ForegroundColor DarkGray
}
function Write-RowBad([string]$name, [string]$detail) {
    Write-Host "  $BAD " -ForegroundColor Red -NoNewline
    Write-Host ('{0,-28} {1}' -f $name, $detail)
}
function Write-RowWarn([string]$name, [string]$detail) {
    Write-Host '  ! ' -ForegroundColor Yellow -NoNewline
    Write-Host ('{0,-28} {1}' -f $name, $detail)
}
function Exit-Setup([string]$title, [string[]]$lines = @()) {
    $script:StepName = ''
    Write-Host ''
    Write-Host "$BAD $title" -ForegroundColor Red
    foreach ($l in $lines) { Write-Host "  $l" }
    exit 1
}
function Close-Step {
    if (-not $script:StepName) { return }
    Write-Note ("$OK {0} done in {1}" -f $script:StepName, (Format-Elapsed $script:StepClock.Elapsed))
    $script:StepName = ''
}
function Write-StepHeader([string]$title) {
    Close-Step
    $script:StepNo++
    $script:StepName = $title
    $script:StepClock = [Diagnostics.Stopwatch]::StartNew()
    Write-Host ''
    Write-Host ('[{0}/{1}] {2}' -f $script:StepNo, $script:StepTotal, $title) -ForegroundColor Blue -NoNewline
    Write-Host (' ({0} elapsed)' -f (Format-Elapsed $Clock.Elapsed)) -ForegroundColor DarkGray
}

# Runs a native command visibly and fails the step when it fails.
function Invoke-Native([string]$exe, [string[]]$arguments = @()) {
    Write-Host ('  $ {0} {1}' -f $exe, ($arguments -join ' ')) -ForegroundColor DarkGray
    & $exe @arguments
    if ($LASTEXITCODE -ne 0) { throw ('"{0} {1}" failed with exit code {2}' -f $exe, ($arguments -join ' '), $LASTEXITCODE) }
}

# Runs a native probe quietly; returns trimmed stdout or $null on failure.
function Get-NativeOutput([string]$exe, [string[]]$arguments = @()) {
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $exe @arguments 2>$null
        if ($LASTEXITCODE -ne 0) { return $null }
        return (($out | Out-String).Trim())
    } catch {
        return $null
    } finally {
        $ErrorActionPreference = $old
    }
}

function Test-Tool([string]$name) { return [bool](Get-Command $name -CommandType Application -ErrorAction SilentlyContinue) }

# Read-Confirmation QUESTION -> $true for yes. Honors -Yes / -NoInstall.
function Read-Confirmation([string]$question) {
    if ($NoInstall) { Write-Note "$question -> skipped (-NoInstall)"; return $false }
    if ($Yes) { Write-Info "$question [Y/n] y (-Yes)"; return $true }
    if ([Console]::IsInputRedirected) {
        Write-Warn "$question -> skipped (no terminal to ask; re-run with -Yes to accept)"
        return $false
    }
    $reply = Read-Host "  $question [Y/n]"
    return ($reply -eq '' -or $reply -match '^(y|yes)$')
}

# Confirm-Install WHAT COMMANDS -> shows exactly what would run, then asks.
function Confirm-Install([string]$what, [string[]]$commands) {
    Write-Info "To install $what this will run:"
    foreach ($c in $commands) { Write-Host "    $c" -ForegroundColor DarkGray }
    return (Read-Confirmation "Install $what now?")
}

# Leading number of each dot-separated part ("1.26.0-rc1" -> 1, 26, 0); missing parts count as 0.
function ConvertTo-VersionPart([string]$version) {
    return @($version.Split('.') | ForEach-Object {
            $digits = $_ -replace '[^0-9].*$', ''
            if ($digits) { [int]$digits } else { 0 }
        })
}

function Test-VersionAtLeast([string]$have, [string]$want) {
    $h = ConvertTo-VersionPart $have
    $w = ConvertTo-VersionPart $want
    for ($i = 0; $i -lt 3; $i++) {
        $x = 0; $y = 0
        if ($i -lt $h.Count) { $x = $h[$i] }
        if ($i -lt $w.Count) { $y = $w[$i] }
        if ($x -gt $y) { return $true }
        if ($x -lt $y) { return $false }
    }
    return $true
}

# ------------------------------------------------------------- environment --
# Reloads PATH from the registry (installers update it there, not in this
# process) and adds the usual tool folders, keeping the existing order first.
function Sync-SessionPath {
    $parts = New-Object System.Collections.Generic.List[string]
    $sources = @($env:Path,
        [Environment]::GetEnvironmentVariable('Path', 'Machine'),
        [Environment]::GetEnvironmentVariable('Path', 'User'),
        (Join-Path $env:USERPROFILE '.cargo\bin'),
        (Join-Path $env:USERPROFILE '.bun\bin'),
        (Join-Path $env:ProgramFiles 'Go\bin'),
        (Join-Path $env:ProgramFiles 'Docker\Docker\resources\bin'))
    foreach ($src in $sources) {
        if (-not $src) { continue }
        foreach ($p in $src.Split(';')) {
            if ($p -and -not $parts.Contains($p) -and (Test-Path $p)) { $parts.Add($p) }
        }
    }
    $env:Path = ($parts -join ';')
}

$script:Arch = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $script:Arch = $env:PROCESSOR_ARCHITEW6432 }
$script:Missing = New-Object System.Collections.Generic.List[string]
$script:WasInstalled = $false
$script:GoVersion = ''

function Show-System {
    $os = 'Windows'
    try { $os = (Get-CimInstance Win32_OperatingSystem).Caption } catch { $null = $_ }
    Write-RowOk 'System' ('{0}, {1}' -f $os, $script:Arch)
    Write-RowOk 'Repository' $Root
}

function Invoke-Winget([string]$id, [string[]]$extra = @()) {
    $wingetArgs = @('install', '-e', '--id', $id, '--accept-source-agreements', '--accept-package-agreements') + $extra
    Invoke-Native 'winget' $wingetArgs
    Sync-SessionPath
}

function Get-WingetCommand([string]$id, [string]$extra = '') {
    $c = "winget install -e --id $id --accept-source-agreements --accept-package-agreements"
    if ($extra) { $c = "$c $extra" }
    return $c
}

function Test-Winget {
    if (Test-Tool 'winget') {
        Write-RowOk 'winget' (Get-NativeOutput 'winget' @('--version'))
        return
    }
    Write-RowBad 'winget' 'not found (Windows package manager; used to install the tools below)'
    Write-Info 'Install "App Installer" from the Microsoft Store: https://aka.ms/getwinget'
    $script:Missing.Add('winget (App Installer) - https://aka.ms/getwinget')
}

$VsWhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
function Get-VcToolsComponent {
    if ($script:Arch -eq 'ARM64') { return 'Microsoft.VisualStudio.Component.VC.Tools.ARM64' }
    return 'Microsoft.VisualStudio.Component.VC.Tools.x86.x64'
}
function Get-VcToolsPath {
    if (-not (Test-Path $VsWhere)) { return $null }
    $p = Get-NativeOutput $VsWhere @('-latest', '-products', '*', '-requires', (Get-VcToolsComponent), '-property', 'installationPath')
    if ($p) { return $p }
    return $null
}

function Test-CppBuildTool {
    $name = 'C++ Build Tools'
    $p = Get-VcToolsPath
    if ($p) { Write-RowOk $name $p; return }
    Write-RowBad $name 'not installed (Microsoft compilers; needed to build the app)'
    $override = '--wait --passive --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended'
    if ($script:Arch -eq 'ARM64') { $override = "$override --add Microsoft.VisualStudio.Component.VC.Tools.ARM64" }
    $cmd = Get-WingetCommand 'Microsoft.VisualStudio.2022.BuildTools' "--override `"$override`""
    if ((Test-Tool 'winget') -and (Confirm-Install 'the Visual Studio C++ Build Tools (large download, asks for admin rights)' @($cmd))) {
        Invoke-Winget 'Microsoft.VisualStudio.2022.BuildTools' @('--override', $override)
        $p = Get-VcToolsPath
    }
    if ($p) {
        $script:WasInstalled = $true
        Write-RowOk $name $p
    } else {
        $script:Missing.Add("Visual Studio C++ Build Tools ('Desktop development with C++') - $TauriPrereqsUrl")
    }
}

function Get-WebView2Version {
    $guid = '{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
    $keys = @("HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\$guid",
        "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\$guid",
        "HKCU:\Software\Microsoft\EdgeUpdate\Clients\$guid")
    foreach ($k in $keys) {
        $item = Get-ItemProperty -Path $k -Name pv -ErrorAction SilentlyContinue
        if ($item -and $item.pv -and $item.pv -ne '0.0.0.0') { return [string]$item.pv }
    }
    return $null
}

function Test-WebView2 {
    $name = 'WebView2 Runtime'
    $v = Get-WebView2Version
    if ($v) { Write-RowOk $name $v; return }
    Write-RowBad $name 'not installed (Microsoft Edge engine the app window uses)'
    if ((Test-Tool 'winget') -and (Confirm-Install 'the WebView2 Runtime' @((Get-WingetCommand 'Microsoft.EdgeWebView2Runtime')))) {
        Invoke-Winget 'Microsoft.EdgeWebView2Runtime'
        $v = Get-WebView2Version
    }
    if ($v) {
        $script:WasInstalled = $true
        Write-RowOk $name $v
    } else {
        $script:Missing.Add('WebView2 Runtime - https://developer.microsoft.com/microsoft-edge/webview2/')
    }
}

# Inside backend\, GOTOOLCHAIN=auto lets an older Go fetch the version go.mod asks for.
function Test-GoVersion {
    $script:GoVersion = ''
    if (-not (Test-Tool 'go')) { return $false }
    Push-Location $Backend
    try { $v = Get-NativeOutput 'go' @('env', 'GOVERSION') } finally { Pop-Location }
    if (-not $v) { return $false }
    $script:GoVersion = $v -replace '^go', ''
    return (Test-VersionAtLeast $script:GoVersion $MinGo)
}

function Test-Go {
    $name = "Go $MinGo+"
    if (Test-GoVersion) {
        $base = Get-NativeOutput 'go' @('env', 'GOVERSION')
        if ("go$($script:GoVersion)" -eq $base) { Write-RowOk $name "go$($script:GoVersion)" }
        else { Write-RowOk $name "go$($script:GoVersion) (auto toolchain; installed $base)" }
        return
    }
    if (Test-Tool 'go') { Write-RowBad $name ('found {0}, too old' -f (Get-NativeOutput 'go' @('env', 'GOVERSION'))) }
    else { Write-RowBad $name 'not installed (compiles the Nimbo backend)' }
    if ((Test-Tool 'winget') -and (Confirm-Install 'Go' @((Get-WingetCommand 'GoLang.Go')))) {
        Invoke-Winget 'GoLang.Go'
    }
    if (Test-GoVersion) {
        $script:WasInstalled = $true
        Write-RowOk $name "go$($script:GoVersion)"
    } else {
        $script:Missing.Add("Go $MinGo or newer - https://go.dev/dl/")
    }
}

function Test-Bun {
    $name = "Bun $MinBun+"
    $v = $null
    if (Test-Tool 'bun') { $v = Get-NativeOutput 'bun' @('--version') }
    $path = ''
    if ($v) { $path = (Get-Command bun -CommandType Application | Select-Object -First 1).Source }
    if ($v -and (Test-VersionAtLeast $v $MinBun)) { Write-RowOk $name "$v ($path)"; return }
    if ($v) {
        Write-RowBad $name "found $v at $path (first on PATH), too old"
        foreach ($c in @(Get-Command bun -CommandType Application -All -ErrorAction SilentlyContinue | Select-Object -Skip 1)) {
            $ov = Get-NativeOutput $c.Source @('--version')
            if ($ov -and (Test-VersionAtLeast $ov $MinBun)) {
                Write-Info ('A newer Bun {0} is installed at {1}, but {2} comes first on your PATH.' -f $ov, $c.Source, $path)
                Write-Info 'Move its folder before the old one in your PATH (System settings > Environment variables), or upgrade/remove the old one.'
            }
        }
        if (Confirm-Install 'a newer Bun' @('bun upgrade')) { Invoke-Native 'bun' @('upgrade') }
    } else {
        Write-RowBad $name 'not installed (JavaScript runtime used to build the app)'
        if ((Test-Tool 'winget') -and (Confirm-Install 'Bun' @((Get-WingetCommand 'Oven-sh.Bun')))) {
            Invoke-Winget 'Oven-sh.Bun'
        }
    }
    $v = $null
    if (Test-Tool 'bun') { $v = Get-NativeOutput 'bun' @('--version') }
    if ($v -and (Test-VersionAtLeast $v $MinBun)) {
        $script:WasInstalled = $true
        Write-RowOk $name ('{0} ({1})' -f $v, (Get-Command bun -CommandType Application | Select-Object -First 1).Source)
    } else {
        $script:Missing.Add("Bun $MinBun or newer - https://bun.sh (the bun first on your PATH is used)")
    }
}

function Get-RustVersion {
    if (-not ((Test-Tool 'rustc') -and (Test-Tool 'cargo'))) { return $null }
    $out = Get-NativeOutput 'rustc' @('--version')
    if ($out -match '^rustc ([0-9][0-9.]*)') { return $Matches[1] }
    return $null
}

function Test-Rust {
    $name = "Rust $MinRust+"
    $v = Get-RustVersion
    if ($v -and (Test-VersionAtLeast $v $MinRust)) { Write-RowOk $name "rustc $v"; return }
    if ($v -and (Test-Tool 'rustup')) {
        Write-RowBad $name "found rustc $v, too old"
        if (Confirm-Install 'the latest stable Rust' @('rustup update stable')) { Invoke-Native 'rustup' @('update', 'stable') }
    } elseif ((Test-Tool 'rustup') -and -not $v) {
        Write-RowBad $name 'rustup is installed but has no toolchain'
        if (Confirm-Install 'the stable Rust toolchain' @('rustup default stable')) { Invoke-Native 'rustup' @('default', 'stable') }
    } elseif ($v) {
        Write-RowBad $name "found rustc $v (not from rustup), too old"
        Write-Info 'Update it with the tool that installed it, or remove it and use https://rustup.rs'
    } else {
        Write-RowBad $name 'not installed (compiles the desktop app shell)'
        if ((Test-Tool 'winget') -and (Confirm-Install 'Rust (rustup)' @((Get-WingetCommand 'Rustlang.Rustup'), 'rustup default stable'))) {
            Invoke-Winget 'Rustlang.Rustup'
            if (Test-Tool 'rustup') { Invoke-Native 'rustup' @('default', 'stable') }
        }
    }
    Sync-SessionPath
    $v = Get-RustVersion
    if ($v -and (Test-VersionAtLeast $v $MinRust)) {
        $script:WasInstalled = $true
        Write-RowOk $name "rustc $v"
    } else {
        $script:Missing.Add("Rust $MinRust or newer - https://rustup.rs")
    }
}

function Test-Wsl {
    $name = 'WSL 2 (for Docker)'
    if (-not (Test-Tool 'wsl')) {
        Write-RowWarn $name 'not available - Docker Desktop needs it; the app guide shows how (wsl --install, as admin, then restart)'
        return
    }
    if ($null -ne (Get-NativeOutput 'wsl' @('--status'))) { Write-RowOk $name 'installed' }
    else { Write-RowWarn $name 'not set up - run "wsl --install" in an admin terminal, then restart (the app guide explains)' }
}

function Test-Docker {
    $name = 'Docker (recommended)'
    if (-not (Test-Tool 'docker')) {
        Write-RowWarn $name ('not found - not needed to build; the app guide helps you install Docker Desktop ({0})' -f (Get-WingetCommand 'Docker.DockerDesktop'))
        return
    }
    $server = Get-NativeOutput 'docker' @('version', '--format', '{{.Server.Version}}')
    if ($server) { Write-RowOk $name ('engine {0}, CLI {1}' -f $server, (Get-Command docker -CommandType Application | Select-Object -First 1).Source) }
    else { Write-RowWarn $name 'CLI found but the engine is not running - start Docker Desktop (the app can start it for you)' }
}

function Test-Prerequisite {
    Sync-SessionPath
    Test-Winget
    Test-CppBuildTool
    Test-WebView2
    Test-Go
    Test-Bun
    Test-Rust
    Test-Wsl
    Test-Docker
    if ($script:Missing.Count -gt 0) {
        $lines = @($script:Missing | ForEach-Object { "- $_" })
        $lines += @('', 'Install them (or re-run .\setup.ps1 without -NoInstall and answer yes), then run .\setup.ps1 again.', 'Step-by-step help: docs/SETUP.md')
        Exit-Setup 'Missing prerequisites:' $lines
    }
    if ($script:WasInstalled) { Write-Note 'Tools were installed in this run; open a new terminal later so it picks them up too.' }
}

function Assert-BuildTool {
    Sync-SessionPath
    $missingTools = @('go', 'bun', 'cargo', 'rustc') | Where-Object { -not (Test-Tool $_) }
    if ($missingTools) { Exit-Setup ('Missing tools: {0}' -f ($missingTools -join ' ')) @('Run .\setup.ps1 (install) first; it checks and installs prerequisites.') }
    if (-not (Test-GoVersion)) { Exit-Setup "Go $MinGo or newer is required." @('Run .\setup.ps1 to install it.') }
}

# ------------------------------------------------------------------ builds --
$script:Version = ''
function Resolve-Version {
    $v = $null
    if (Test-Tool 'git') { $v = Get-NativeOutput 'git' @('-C', $Root, 'describe', '--tags', '--always', '--dirty') }
    if (-not $v) {
        $conf = Get-Content (Join-Path $TauriDir 'tauri.conf.json') -Raw | ConvertFrom-Json
        $v = '{0}-dev' -f $conf.version
    }
    $script:Version = $v
    $env:NIMBO_VERSION = $v
}

function Install-Dependency {
    Push-Location $Desktop
    try {
        $locked = (Test-Path 'bun.lock') -or (Test-Path 'bun.lockb')
        $ok = $false
        if ($locked) {
            try { Invoke-Native 'bun' @('install', '--frozen-lockfile'); $ok = $true }
            catch { Write-Warn 'The lockfile is out of date with package.json; installing without --frozen-lockfile.' }
        }
        if (-not $ok) { Invoke-Native 'bun' @('install') }
    } finally { Pop-Location }
}

function Build-Backend {
    Push-Location $Backend
    try {
        New-Item -ItemType Directory -Force -Path (Split-Path $NimboBin) | Out-Null
        Invoke-Native 'go' @('build', '-trimpath', '-ldflags', "-X main.version=$($script:Version)", '-o', $NimboBin, './cmd/nimbo')
    } finally { Pop-Location }
    Write-RowOk 'backend\bin\nimbo.exe' (Get-NativeOutput $NimboBin @('version'))
}

function Test-BackendStale {
    if (-not (Test-Path $NimboBin)) { return $true }
    $built = (Get-Item $NimboBin).LastWriteTimeUtc
    $newer = Get-ChildItem -Path $Backend -Recurse -File -Include '*.go', 'go.mod', 'go.sum' -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTimeUtc -gt $built } | Select-Object -First 1
    return [bool]$newer
}

function Build-Sidecar {
    Push-Location $Desktop
    try { Invoke-Native 'bun' @('run', 'sidecar') } finally { Pop-Location }
}

$script:Bundles = @()
function Get-BundleDir {
    $target = $env:CARGO_TARGET_DIR
    if (-not $target) { $target = Join-Path $TauriDir 'target' }
    elseif (-not [IO.Path]::IsPathRooted($target)) { $target = Join-Path $TauriDir $target }
    return (Join-Path $target 'release\bundle')
}

function Build-App {
    $started = (Get-Date).ToUniversalTime().AddSeconds(-2)
    Push-Location $Desktop
    try { Invoke-Native 'bun' @('run', 'tauri', 'build') } finally { Pop-Location }
    $dir = Get-BundleDir
    $script:Bundles = @(Get-ChildItem -Path $dir -Recurse -Depth 1 -File -Include '*.msi', '*.exe' -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTimeUtc -ge $started } | Sort-Object FullName | ForEach-Object { $_.FullName })
    if ($script:Bundles.Count -eq 0) { Exit-Setup "The build finished but no installer was found in $dir." @('Check the Tauri output above.') }
    Write-Info 'Built:'
    foreach ($b in $script:Bundles) { Write-Info "  $b" }
}

# ------------------------------------------------------------ install offers --
$script:InstalledDir = ''
function Get-InstalledAppDir {
    $d = Join-Path $env:LOCALAPPDATA 'Nimbo'
    if (Test-Path (Join-Path $d 'nimbo.exe')) { return $d }
    $d = Join-Path $env:ProgramFiles 'Nimbo'
    if (Test-Path (Join-Path $d 'nimbo.exe')) { return $d }
    return ''
}

function Invoke-AppInstall {
    $setupExe = $script:Bundles | Where-Object { $_ -like '*-setup.exe' } | Select-Object -First 1
    if ($setupExe) {
        if (Read-Confirmation ('Run the Nimbo installer now ({0})?' -f (Split-Path $setupExe -Leaf))) {
            Write-Host ('  $ "{0}"' -f $setupExe) -ForegroundColor DarkGray
            Start-Process -FilePath $setupExe -Wait
        }
    }
    $script:InstalledDir = Get-InstalledAppDir
    $cliDir = Split-Path $NimboBin
    if ($script:InstalledDir) { $cliDir = $script:InstalledDir }
    Write-Info "The 'nimbo' command lets AI agents use Nimbo over MCP (see docs/MCP.md)."
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) { $userPath = '' }
    if (@($userPath.Split(';')) -contains $cliDir) {
        Write-RowOk 'CLI' "$cliDir is already on your PATH"
    } elseif (Read-Confirmation "Add $cliDir to your user PATH so 'nimbo' works in new terminals?") {
        $newPath = (@($userPath.Split(';') | Where-Object { $_ }) + $cliDir) -join ';'
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-RowOk 'CLI' "$cliDir added to your user PATH (open a new terminal)"
    }
}

# ------------------------------------------------------------------ doctor --
$script:DoctorExit = 0
function Invoke-Doctor {
    $doctorArgs = @('doctor')
    if ($Json) { $doctorArgs += '--json' }
    & $NimboBin @doctorArgs
    $script:DoctorExit = $LASTEXITCODE
    if ($script:DoctorExit -eq 2) { Write-Warn "This nimbo build has no 'doctor' command; rebuild with .\setup.ps1 build." }
}

function Show-NextStepList {
    Write-Host ''
    Write-Host ("$OK Nimbo is built in {0}." -f (Format-Elapsed $Clock.Elapsed)) -ForegroundColor Green
    Write-Host ''
    Write-Host 'Next steps'
    if ($script:InstalledDir) { Write-Info '1. Open Nimbo from the Start menu.' }
    else { Write-Info '1. Install Nimbo with the installer listed above, then open it from the Start menu.' }
    Write-Info '2. The first-run guide checks WSL 2, Docker Desktop and the sandbox images and fixes what it can.'
    if ($script:DoctorExit -ne 0) { Write-Info '   (nimbo doctor above lists what is still missing - the guide walks you through it.)' }
    Write-Info '3. Development mode with hot reload: .\setup.ps1 dev'
    Write-Info 'Check this computer again any time: .\setup.ps1 doctor'
}

# -------------------------------------------------------------------- main --
Set-Location $Root
if ($Command -ne 'doctor') { Write-Host ("Nimbo setup - {0} ({1})" -f $Command, $Root) }

try {
    switch ($Command) {
        'install' {
            $script:StepTotal = 8
            Write-StepHeader 'Detecting this computer'; Show-System
            Write-StepHeader 'Checking prerequisites'; Test-Prerequisite
            Resolve-Version
            Write-StepHeader 'Installing JavaScript dependencies'; Install-Dependency
            Write-StepHeader "Building the backend ($($script:Version))"; Build-Backend
            Write-StepHeader "Building the app's bundled backend (sidecar)"; Build-Sidecar
            Write-StepHeader 'Building the desktop app (takes a few minutes the first time)'; Build-App
            Write-StepHeader 'Installing'; Invoke-AppInstall
            Write-StepHeader 'Checking Docker and sandbox readiness (nimbo doctor)'; Invoke-Doctor
            Close-Step
            Show-NextStepList
        }
        'dev' {
            $script:StepTotal = 6
            Write-StepHeader 'Detecting this computer'; Show-System
            Write-StepHeader 'Checking prerequisites'; Test-Prerequisite
            Resolve-Version
            Write-StepHeader 'Installing JavaScript dependencies'; Install-Dependency
            Write-StepHeader "Building the backend ($($script:Version))"; Build-Backend
            Write-StepHeader "Building the app's bundled backend (sidecar)"; Build-Sidecar
            Write-StepHeader 'Starting the app in development mode (Ctrl+C to stop)'
            Close-Step
            Set-Location $Desktop
            Invoke-Native 'bun' @('run', 'tauri', 'dev')
        }
        'build' {
            $script:StepTotal = 4
            Assert-BuildTool
            Resolve-Version
            Write-StepHeader 'Installing JavaScript dependencies'; Install-Dependency
            Write-StepHeader "Building the backend ($($script:Version))"; Build-Backend
            Write-StepHeader "Building the app's bundled backend (sidecar)"; Build-Sidecar
            Write-StepHeader 'Building the desktop app'; Build-App
            Close-Step
            Write-Host ''
            Write-Host ("$OK Build finished in {0}." -f (Format-Elapsed $Clock.Elapsed)) -ForegroundColor Green
        }
        'doctor' {
            Sync-SessionPath
            if (Test-BackendStale) {
                if (-not (Test-GoVersion)) { Exit-Setup "Go $MinGo or newer is required to build the backend." @('Run .\setup.ps1 first.') }
                Resolve-Version
                $script:StepTotal = 1
                # Keep stdout for nimbo's own report (it may be -Json).
                Write-StepHeader "Building the backend ($($script:Version))"; Build-Backend
                Close-Step
            }
            Invoke-Doctor
            exit $script:DoctorExit
        }
    }
} catch {
    $failed = $script:StepName
    Write-Host ''
    Write-Host ("$BAD {0}" -f $_.Exception.Message) -ForegroundColor Red
    if ($failed) {
        Write-Host ('  Step "{0}" failed after {1}. See the messages above; docs/SETUP.md has troubleshooting tips.' -f $failed, (Format-Elapsed $script:StepClock.Elapsed))
    }
    exit 1
}
