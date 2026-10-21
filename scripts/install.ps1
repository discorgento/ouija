# Installs ouija on Windows from a GitHub release: downloads the archive,
# checks it against the release's SHA256SUMS, puts ouija.exe and
# ouija-mcp.exe in a folder of your user and adds it to your PATH.
#
#   irm https://raw.githubusercontent.com/discorgento/ouija/main/scripts/install.ps1 | iex
#
# Or, saved, with options:
#   .\install.ps1 -Version 0.2.0 -InstallDir C:\tools\ouija -NoPath -NoPrompt
#
# Nothing needs administrator rights: the folder and the PATH are your
# user's. ouija is a terminal program - after this, open a new terminal and
# run `ouija`.
param(
    [string]$Version = "latest",
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\ouija"),
    [switch]$NoPath,
    [switch]$NoPrompt, # ask nothing (no setup); CI never asks
    [switch]$RequireProvenance # refuse to install unless the build provenance is verified (needs gh)
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue" # the progress bar slows downloads a lot
$repo = "discorgento/ouija"

if ($env:PROCESSOR_ARCHITECTURE -ne "AMD64") {
    throw "ouija ships Windows builds for x64 (amd64) only; this machine is $env:PROCESSOR_ARCHITECTURE."
}

# The release and its assets. "latest" is the newest release that carries
# them: for a few minutes after a release is cut, CI is still building its
# archives, and the newest release has none yet.
function Find-Assets($rel) {
    # ouija_<version>_windows_amd64.zip; v0.1.0 shipped ouija-windows-amd64.tar.gz.
    $a = $rel.assets | Where-Object { $_.name -match '^ouija_.+_windows_amd64\.zip$' } | Select-Object -First 1
    if (-not $a) {
        $a = $rel.assets | Where-Object { $_.name -eq "ouija-windows-amd64.tar.gz" } | Select-Object -First 1
    }
    $s = $rel.assets | Where-Object { $_.name -eq "SHA256SUMS" } | Select-Object -First 1
    if ($a -and $s) { return @($a, $s) }
    return $null
}

$headers = @{ "User-Agent" = "ouija-install" }
# A token, when there is one, lifts the API's limit for anonymous calls
# (shared CI machines hit it).
if ($env:GITHUB_TOKEN) { $headers["Authorization"] = "Bearer $env:GITHUB_TOKEN" }

$release = $null
$found = $null
if ($Version -eq "latest") {
    $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases?per_page=10" -Headers $headers
    foreach ($r in $releases) {
        if ($r.draft -or $r.prerelease) { continue }
        $found = Find-Assets $r
        if ($found) { $release = $r; break }
        Write-Host "$($r.tag_name) has no Windows archive yet (still being built?); trying the release before it"
    }
    if (-not $release) { throw "no release with a Windows archive and SHA256SUMS found." }
} else {
    $tag = "v" + $Version.TrimStart('v')
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/tags/$tag" -Headers $headers
    $found = Find-Assets $release
    if (-not $found) { throw "release $tag has no Windows archive or no SHA256SUMS (if it was cut minutes ago, CI may still be building it)." }
}
$asset, $sums = $found
Write-Host "ouija $($release.tag_name) - $($asset.name)"

# Download and verify.
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("ouija-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $archive = Join-Path $tmp $asset.name
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $archive -UseBasicParsing
    $sumsFile = Join-Path $tmp "SHA256SUMS"
    Invoke-WebRequest -Uri $sums.browser_download_url -OutFile $sumsFile -UseBasicParsing

    $line = Get-Content $sumsFile | Where-Object { $_ -match ("\s\*?" + [regex]::Escape($asset.name) + '$') } | Select-Object -First 1
    if (-not $line) { throw "$($asset.name) is not listed in SHA256SUMS." }
    $want = ($line -split '\s+')[0].ToLowerInvariant()
    $got = (Get-FileHash -Algorithm SHA256 -Path $archive).Hash.ToLowerInvariant()
    if ($got -ne $want) { throw "checksum mismatch for $($asset.name): got $got, want $want - not installed." }
    Write-Host "checksum ok"

    # The checksum only proves the zip matches SHA256SUMS, and both come from
    # the same release. The build provenance ties the zip to this repository's
    # release workflow (Sigstore-signed by GitHub Actions): a swapped zip has
    # none. Checking it needs the GitHub CLI, logged in or with a token.
    $canVerify = $false
    if (Get-Command gh -ErrorAction SilentlyContinue) {
        if ($env:GH_TOKEN -or $env:GITHUB_TOKEN) {
            $canVerify = $true
        } else {
            & gh auth status *> $null
            $canVerify = ($LASTEXITCODE -eq 0)
        }
    }
    if ($canVerify) {
        & gh attestation verify $archive --repo $repo *> $null
        if ($LASTEXITCODE -ne 0) { throw "provenance check failed for $($asset.name): it was not built by $repo's release workflow - not installed." }
        Write-Host "provenance ok (built by $repo's release workflow)"
    } elseif ($RequireProvenance) {
        throw "-RequireProvenance: the GitHub CLI (gh), logged in, is needed to verify the build provenance - not installed."
    } else {
        Write-Host "provenance not checked (with the GitHub CLI logged in, the installer verifies it: gh attestation verify <zip> --repo $repo)"
    }

    # Extract (tar ships with Windows 10+ for the old .tar.gz).
    $out = Join-Path $tmp "x"
    New-Item -ItemType Directory -Path $out | Out-Null
    if ($asset.name.EndsWith(".zip")) {
        Expand-Archive -Path $archive -DestinationPath $out
    } else {
        tar -xzf $archive -C $out
        if ($LASTEXITCODE -ne 0) { throw "could not extract $($asset.name)." }
    }
    $exe = Get-ChildItem -Path $out -Recurse -Filter "ouija.exe" | Select-Object -First 1
    if (-not $exe) { throw "no ouija.exe in $($asset.name)." }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Force -Path (Join-Path $exe.DirectoryName "*.exe") -Destination $InstallDir
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
Write-Host "installed in $InstallDir"

# The user's PATH (no admin), and this session's.
if (-not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $entries = @($userPath -split ';' | Where-Object { $_ -ne "" })
    if ($entries -notcontains $InstallDir) {
        [Environment]::SetEnvironmentVariable("Path", (($entries + $InstallDir) -join ';'), "User")
        Write-Host "added $InstallDir to your PATH"
    }
    if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path = "$env:Path;$InstallDir" }
}

& (Join-Path $InstallDir "ouija.exe") version
# In a console, offer to run the setup right away; never in CI.
$interactive = -not $NoPrompt -and -not $env:CI -and [Environment]::UserInteractive -and -not [Console]::IsInputRedirected
Write-Host ""
if ($interactive) {
    $answer = Read-Host "Set up ouija now (provider, API key, model)? [Y/n]"
    if ($answer -eq "" -or $answer -match '^(y|yes|s|sim)$') {
        & (Join-Path $InstallDir "ouija.exe") setup
        Write-Host ""
        Write-Host "Next: open a new terminal, cd into a project and run ``ouija``."
        Write-Host "The bash tool needs Git for Windows (its sh.exe): https://gitforwindows.org"
        return
    }
}
Write-Host "Next: open a new terminal, then"
Write-Host "  ouija setup      # provider, API key and default model (or Ollama: local and free)"
Write-Host "  ouija            # in a project folder: the chat"
Write-Host "The bash tool needs Git for Windows (its sh.exe): https://gitforwindows.org"
