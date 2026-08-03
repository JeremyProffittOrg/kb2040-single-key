<#
.SYNOPSIS
    Copies the firmware in src/ onto an attached KB2040 running CircuitPython.

.DESCRIPTION
    Finds the CIRCUITPY volume, installs the CircuitPython libraries the firmware needs
    with circup, then mirrors src/ onto the drive.

    This is not a "deploy" in the sense deploy.md forbids -- it writes to a USB mass-storage
    volume plugged into this machine, not to a cloud account. Nothing here touches AWS and
    nothing here needs a credential.

.PARAMETER Drive
    The CIRCUITPY drive letter (for example H:). Detected automatically when omitted.

.PARAMETER SkipLibraries
    Do not run circup. Useful when the libraries are already installed and the board is
    only being re-flashed with changed source.

.EXAMPLE
    pwsh scripts/flash.ps1

.EXAMPLE
    pwsh scripts/flash.ps1 -Drive H: -SkipLibraries
#>
[CmdletBinding()]
param(
    [string]$Drive,
    [switch]$SkipLibraries
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$srcRoot = Join-Path $repoRoot 'src'

if (-not (Test-Path $srcRoot)) {
    throw "No src/ directory at $srcRoot. Run this from a checkout of kb2040-single-key."
}

# --- locate the board -------------------------------------------------------------------

if (-not $Drive) {
    $volume = Get-Volume | Where-Object { $_.FileSystemLabel -eq 'CIRCUITPY' } | Select-Object -First 1
    if (-not $volume) {
        throw @"
No CIRCUITPY drive found.

  * Is the board plugged in?
  * Does it have CircuitPython on it? A board in bootloader mode shows up as RPI-RP2
    instead; copy a CircuitPython .uf2 onto that first.
    https://circuitpython.org/board/adafruit_kb2040/
"@
    }
    $Drive = "$($volume.DriveLetter):"
}

if (-not (Test-Path $Drive)) {
    throw "Drive $Drive is not available."
}

$bootOut = Join-Path $Drive 'boot_out.txt'
if (-not (Test-Path $bootOut)) {
    throw "$Drive does not look like a CircuitPython drive (no boot_out.txt)."
}

Write-Host "Board on $Drive" -ForegroundColor Cyan
Get-Content $bootOut | ForEach-Object { Write-Host "  $_" }

# --- libraries --------------------------------------------------------------------------

# Not vendored into the repo: circup installs the build matching the board's CircuitPython
# version, which is the thing that actually has to match.
$libraries = @('adafruit_hid', 'neopixel', 'adafruit_pixelbuf')

if ($SkipLibraries) {
    Write-Host "`nSkipping library install (-SkipLibraries)." -ForegroundColor Yellow
} else {
    if (-not (Get-Command circup -ErrorAction SilentlyContinue)) {
        throw "circup is not installed. Run: pip install -r requirements-dev.txt"
    }
    Write-Host "`nInstalling libraries: $($libraries -join ', ')" -ForegroundColor Cyan
    & circup --path $Drive install @libraries
    if ($LASTEXITCODE -ne 0) {
        throw "circup failed with exit code $LASTEXITCODE."
    }
}

# --- firmware ---------------------------------------------------------------------------

Write-Host "`nCopying firmware" -ForegroundColor Cyan

foreach ($file in @('boot.py', 'code.py')) {
    $source = Join-Path $srcRoot $file
    Copy-Item -Path $source -Destination (Join-Path $Drive $file) -Force
    Write-Host "  $file"
}

$packageSource = Join-Path $srcRoot 'singlekey'
$packageTarget = Join-Path $Drive 'singlekey'

# Remove first so a module deleted from the repo does not linger on the board and get
# imported in preference to what is actually in src/.
if (Test-Path $packageTarget) {
    Remove-Item -Path $packageTarget -Recurse -Force
}

# Copy the modules individually rather than with `Copy-Item -Recurse` on the directory.
# Two reasons, both of which bite in practice on a CircuitPython volume:
#   * if $packageTarget still exists at this point -- the FAT driver does not always retire
#     the directory entry as promptly as Remove-Item returns -- then -Recurse copies the
#     source *into* it and produces singlekey/singlekey, which fails on the next write.
#   * the repo's src/singlekey/__pycache__ holds host CPython .pyc files. They are useless
#     to CircuitPython and just consume space on a 7 MB drive.
New-Item -ItemType Directory -Path $packageTarget -Force | Out-Null
Get-ChildItem -Path $packageSource -Filter '*.py' -File | ForEach-Object {
    Copy-Item -Path $_.FullName -Destination (Join-Path $packageTarget $_.Name) -Force
    Write-Host "  singlekey/$($_.Name)"
}

Write-Host @"

Done.

boot.py only takes effect after a hard reset, so unplug and replug the board (or press
reset) before configuring it. You should then see two serial ports.

Next:
  go run ./cli/cmd/kb2040ctl ports
  go run ./cli/cmd/kb2040ctl info
"@ -ForegroundColor Green
