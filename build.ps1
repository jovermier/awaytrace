param([string]$FFmpegPath = '')
$ErrorActionPreference = 'Stop'
$projectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -LiteralPath $projectDir
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go 1.25 or newer and add go.exe to PATH.' }
if (-not (Test-Path -LiteralPath 'assets\ffmpeg.exe.xz')) {
    if (-not $FFmpegPath) { throw 'Supply -FFmpegPath with the pinned FFmpeg executable; see README.md.' }
    & go run -buildvcs=false ./cmd/packencoder -input $FFmpegPath -output 'assets/ffmpeg.exe.xz'
    if ($LASTEXITCODE -ne 0) { throw 'Preparing FFmpeg failed.' }
}
New-Item -ItemType Directory -Force -Path dist | Out-Null
$env:CGO_ENABLED = '0'
& go build -buildvcs=false -trimpath -ldflags='-s -w -H=windowsgui' -o dist\AwayTrace.exe .
if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
Write-Host 'Application built: dist\AwayTrace.exe'
