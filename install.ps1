# Aether Windows Installer
# Run with: irm https://raw.githubusercontent.com/Orbis-Mind/aether/main/install.ps1 | iex

param(
    [string]$Version = "latest",
    [string]$InstallDir = "C:\Program Files\Aether"
)

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "  Aether AI Compiler and Runtime — Installer" -ForegroundColor Cyan
Write-Host "  ===========================================" -ForegroundColor Cyan
Write-Host ""

# Get latest version if not specified
if ($Version -eq "latest") {
    Write-Host "  Fetching latest version..." -ForegroundColor Blue
    $release = Invoke-RestMethod "https://api.github.com/repos/Orbis-Mind/aether/releases/latest"
    $Version = $release.tag_name
}

Write-Host "  Version : $Version" -ForegroundColor Blue
Write-Host "  Location: $InstallDir" -ForegroundColor Blue
Write-Host ""

# Download URL
$url = "https://github.com/Orbis-Mind/aether/releases/download/$Version/aether.exe"

# Create install directory
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
}

# Download binary
Write-Host "  Downloading aether.exe..." -ForegroundColor Blue
$exePath = Join-Path $InstallDir "aether.exe"

try {
    Invoke-WebRequest -Uri $url -OutFile $exePath -UseBasicParsing
} catch {
    Write-Host "  ERROR: Failed to download from $url" -ForegroundColor Red
    Write-Host "  Please download manually from: https://github.com/Orbis-Mind/aether/releases" -ForegroundColor Yellow
    exit 1
}

# Add to PATH
$currentPath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
if ($currentPath -notlike "*$InstallDir*") {
    Write-Host "  Adding to system PATH..." -ForegroundColor Blue
    try {
        [Environment]::SetEnvironmentVariable(
            "PATH",
            "$currentPath;$InstallDir",
            "Machine"
        )
        Write-Host "  Added to PATH successfully" -ForegroundColor Green
    } catch {
        Write-Host "  Could not add to system PATH (need admin rights)" -ForegroundColor Yellow
        Write-Host "  Add manually: $InstallDir" -ForegroundColor Yellow
    }
}

Write-Host ""
Write-Host "  Installation complete!" -ForegroundColor Green
Write-Host ""
Write-Host "  Open a new terminal and run:" -ForegroundColor White
Write-Host "    aether --help" -ForegroundColor Cyan
Write-Host "    aether init my-project" -ForegroundColor Cyan
Write-Host "    aether build" -ForegroundColor Cyan
Write-Host ""
