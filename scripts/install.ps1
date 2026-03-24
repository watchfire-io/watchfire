# Watchfire installer for Windows
# Usage: irm https://raw.githubusercontent.com/watchfire-io/watchfire/main/scripts/install.ps1 | iex
$ErrorActionPreference = "Stop"

$Repo = "watchfire-io/watchfire"
$InstallDir = if ($env:WATCHFIRE_INSTALL_DIR) { $env:WATCHFIRE_INSTALL_DIR } else { "$env:LOCALAPPDATA\Watchfire" }

function Write-Info($msg)  { Write-Host "  $msg" -ForegroundColor Cyan }
function Write-Ok($msg)    { Write-Host "  $msg" -ForegroundColor Green }
function Write-Warn($msg)  { Write-Host "  $msg" -ForegroundColor Yellow }

# Detect architecture
function Get-Arch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        "X64"   { return "amd64" }
        "Arm64" { return "arm64" }
        default { throw "Unsupported architecture: $arch" }
    }
}

# Fetch latest version
function Get-LatestVersion {
    $url = "https://api.github.com/repos/$Repo/releases/latest"
    $release = Invoke-RestMethod -Uri $url -Headers @{ "User-Agent" = "watchfire-installer" }
    return $release.tag_name -replace "^v", ""
}

Write-Host ""
Write-Host "  Watchfire Installer" -ForegroundColor Cyan
Write-Host ""

$Arch = Get-Arch
$Version = Get-LatestVersion

Write-Info "OS: windows | Arch: $Arch | Version: v$Version"
Write-Info "Install directory: $InstallDir"

# Create install directory
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$BaseUrl = "https://github.com/$Repo/releases/download/v$Version"
$CliName = "watchfire-windows-$Arch.exe"
$DaemonName = "watchfired-windows-$Arch.exe"

# Download binaries
Write-Info "Downloading watchfire v$Version..."

$TempDir = New-TemporaryFile | ForEach-Object { Remove-Item $_; New-Item -ItemType Directory -Path $_ }
try {
    Invoke-WebRequest -Uri "$BaseUrl/$CliName" -OutFile "$TempDir\watchfire.exe" -UseBasicParsing
    Invoke-WebRequest -Uri "$BaseUrl/$DaemonName" -OutFile "$TempDir\watchfired.exe" -UseBasicParsing
}
catch {
    throw "Download failed: $_"
}

# Install
Write-Info "Installing to $InstallDir..."
Move-Item -Force "$TempDir\watchfire.exe" "$InstallDir\watchfire.exe"
Move-Item -Force "$TempDir\watchfired.exe" "$InstallDir\watchfired.exe"
Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue

# Verify
$ver = & "$InstallDir\watchfire.exe" version 2>&1
if ($LASTEXITCODE -eq 0) {
    Write-Ok "watchfire v$Version installed successfully!"
}
else {
    throw "Installation failed - binary not executable"
}

# Add to PATH if not already there
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    Write-Warn "$InstallDir is not in your PATH. Adding it now..."
    [Environment]::SetEnvironmentVariable("Path", "$InstallDir;$UserPath", "User")
    $env:Path = "$InstallDir;$env:Path"
    Write-Ok "Added $InstallDir to user PATH (restart your terminal for full effect)"
}

Write-Host ""
Write-Host "  Get started:" -ForegroundColor Green
Write-Host "    cd your-project"
Write-Host "    watchfire init"
Write-Host "    watchfire run"
Write-Host ""
