param(
  [string]$Version = $env:MUSIC2BB_VERSION,
  [string]$InstallDir = $env:MUSIC2BB_INSTALL_DIR
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = if ($env:MUSIC2BB_REPOSITORY) { $env:MUSIC2BB_REPOSITORY } else { 'bagags/music2bb-go' }
$releaseOrigin = if ($env:MUSIC2BB_RELEASE_ORIGIN) { $env:MUSIC2BB_RELEASE_ORIGIN.TrimEnd('/') } else { 'https://github.com' }
if (-not $InstallDir) {
  if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not available' }
  $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\music2bb'
}

$architecture = $null
try {
  $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
} catch {
  $architecture = $env:PROCESSOR_ARCHITEW6432
  if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
  $architecture = $architecture.ToLowerInvariant()
}
switch ($architecture) {
  { $_ -in @('x64', 'amd64') } { $goArchitecture = 'amd64'; break }
  { $_ -in @('arm64') } { $goArchitecture = 'arm64'; break }
  default { throw "Unsupported Windows architecture: $architecture" }
}

$package = "music2bb-windows-$goArchitecture"
$archive = "$package.zip"
$releaseBase = if ($Version) {
  "$releaseOrigin/$repository/releases/download/$Version"
} else {
  "$releaseOrigin/$repository/releases/latest/download"
}
$temporaryDir = Join-Path ([IO.Path]::GetTempPath()) ("music2bb-install-" + [Guid]::NewGuid().ToString('N'))

try {
  New-Item -ItemType Directory -Path $temporaryDir | Out-Null
  $archivePath = Join-Path $temporaryDir $archive
  $checksumPath = "$archivePath.sha256"
  Write-Host "Downloading $archive..."
  Invoke-WebRequest -Uri "$releaseBase/$archive" -OutFile $archivePath
  Invoke-WebRequest -Uri "$releaseBase/$archive.sha256" -OutFile $checksumPath

  $expectedChecksum = ((Get-Content -LiteralPath $checksumPath -Raw).Trim() -split '\s+')[0].ToLowerInvariant()
  if ($expectedChecksum -notmatch '^[0-9a-f]{64}$') { throw 'Invalid release checksum' }
  $actualChecksum = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actualChecksum -ne $expectedChecksum) { throw 'Release checksum mismatch' }

  $extractDir = Join-Path $temporaryDir 'extract'
  Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir
  $sourceDir = Join-Path $extractDir $package
  $sourceExecutable = Join-Path $sourceDir 'music2bb.exe'
  if (-not (Test-Path -LiteralPath $sourceExecutable -PathType Leaf)) {
    throw 'Release archive does not contain music2bb.exe'
  }

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Get-ChildItem -LiteralPath $sourceDir -File | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $InstallDir $_.Name) -Force
  }

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $pathEntries = @($userPath -split ';' | Where-Object { $_ })
  if (-not ($pathEntries | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) {
    $newUserPath = if ($userPath) { "$InstallDir;$userPath" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable('Path', $newUserPath, 'User')
  }
  if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) {
    $env:Path = "$InstallDir;$env:Path"
  }

  Write-Host "Installed music2bb to $(Join-Path $InstallDir 'music2bb.exe')"
  Write-Host 'The user PATH has been updated; music2bb is available in this and future PowerShell sessions.'
} finally {
  if (Test-Path -LiteralPath $temporaryDir) {
    Remove-Item -LiteralPath $temporaryDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}
