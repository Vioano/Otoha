$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
if (-not [Environment]::Is64BitOperatingSystem) { throw 'Otoha requires 64-bit Windows.' }
$otohaDirectory = Join-Path $env:LOCALAPPDATA 'Otoha'
New-Item -ItemType Directory -Force -Path $otohaDirectory | Out-Null
$otohaTemporary = Join-Path $otohaDirectory 'otoha-download.zip'
$otohaExecutable = Join-Path $otohaDirectory 'otoha.exe'
# First Otoha binary release is not published yet. Existing source-built binaries
# can be used; downloading requires an explicitly published Otoha release.
$otohaRelease = [version]'0.1.0'
$otohaReuse = $false
if (Test-Path -LiteralPath $otohaExecutable -PathType Leaf) {
    try {
        $otohaInstalledVersion = & $otohaExecutable version
        if ($LASTEXITCODE -eq 0 -and $otohaInstalledVersion -match '^otoha (\d+\.\d+\.\d+)$') {
            $otohaReuse = [version]$Matches[1] -ge $otohaRelease
        }
    } catch { $otohaReuse = $false }
}
$otohaURL = 'https://github.com/Vioano/Otoha/releases/download/v0.1.0/otoha-windows-amd64.zip'
if ($env:OTOHA_DOWNLOAD_BASE) { $otohaURL = $env:OTOHA_DOWNLOAD_BASE.TrimEnd('/') + '/otoha-windows-amd64.zip' }
if (-not $otohaReuse) {
    if (-not $env:OTOHA_DOWNLOAD_BASE) { throw 'No Otoha binary release is published yet. Build apps/cli from source; do not install a historical product binary.' }
    Invoke-WebRequest -UseBasicParsing -Uri $otohaURL -OutFile $otohaTemporary -Headers @{'User-Agent'='Otoha-CLI/0.1.0'}
    Expand-Archive -Force -Path $otohaTemporary -DestinationPath $otohaDirectory
    & $otohaExecutable version
    if ($LASTEXITCODE -ne 0) { throw 'Downloaded Otoha did not run.' }
    Remove-Item $otohaTemporary
}
$otohaUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($otohaUserPath -split ';') -notcontains $otohaDirectory) {
    [Environment]::SetEnvironmentVariable('Path', ($otohaDirectory + ';' + $otohaUserPath), 'User')
}
$env:Path = $otohaDirectory + ';' + $env:Path
if ($otohaReuse) { Write-Output ('Using existing ' + $otohaExecutable) }
else { Write-Output ('Installed ' + $otohaExecutable) }
