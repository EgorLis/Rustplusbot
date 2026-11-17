param(
    [ValidatePattern('^[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$')]
    [string]$Version = '1.1',
    [string]$GoCommand = 'go',
    [string]$NodeCommand = 'node'
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$releaseDir = Join-Path $projectRoot ".local/releases/$Version"
$stage = Join-Path $releaseDir ('package-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
$archiveName = "RustPlusBot-$Version-windows-amd64.zip"
$archive = Join-Path $releaseDir $archiveName

if (Test-Path -LiteralPath $archive) {
    throw "Release archive already exists: $archive"
}

$savedEnv = @{}
foreach ($key in @('GOOS', 'GOARCH', 'CGO_ENABLED', 'RUSTPLUS_LIVE_CONFIG')) {
    $savedEnv[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}

Push-Location $projectRoot

try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    Remove-Item Env:RUSTPLUS_LIVE_CONFIG -ErrorAction SilentlyContinue

    & $GoCommand test -timeout 30s ./...
    if ($LASTEXITCODE -ne 0) {
        throw 'Release tests failed.'
    }

    $frontendTests = @(Get-ChildItem -LiteralPath "$projectRoot/internal/webui/test" -Filter '*.test.cjs' | ForEach-Object FullName)
    & $NodeCommand --test @frontendTests
    if ($LASTEXITCODE -ne 0) {
        throw 'Frontend tests failed.'
    }

    New-Item -ItemType Directory -Path "$stage/conf", "$stage/sounds" -Force | Out-Null

    & $GoCommand build -trimpath -ldflags "-s -w -X main.version=$Version" -o "$stage/main.exe" ./cmd/rustplusbot
    if ($LASTEXITCODE -ne 0) {
        throw 'Release build failed.'
    }

    $reportedVersion = & "$stage/main.exe" --version
    if ($LASTEXITCODE -ne 0 -or $reportedVersion -ne "RustPlusBot $Version") {
        throw 'Release executable reported an unexpected version.'
    }

    Copy-Item -LiteralPath "$projectRoot/configs/rpconfig.example.json" -Destination "$stage/conf/rpconfig.json"
    Copy-Item -LiteralPath "$projectRoot/configs/bmconfig.example.json" -Destination "$stage/conf/bmconfig.json"
    [IO.File]::WriteAllText("$stage/conf/botconfig.json", "{`n  `"alarms`": {},`n  `"players`": []`n}`n")

    Copy-Item -LiteralPath "$projectRoot/CONNECTION.md" -Destination "$stage/CONNECTION.md"
    Copy-Item -LiteralPath "$projectRoot/releases/$Version.md" -Destination "$stage/RELEASE-NOTES.md"
    $readme = "$projectRoot/releases/README.txt"
    if ($Version -like '*-webui*') {
        $readme = "$projectRoot/releases/README-WebUI.txt"
    }
    Copy-Item -LiteralPath $readme -Destination "$stage/README.txt"
    Copy-Item -LiteralPath "$projectRoot/WEBUI.md" -Destination "$stage/WEBUI.md"
    [IO.File]::WriteAllText("$stage/sounds/README.txt", "Place your own 1.mp3, 2.mp3, 3.mp3 or other sound files here.`r`n")
    [IO.File]::WriteAllText("$stage/Start-RustBot.cmd", "@echo off`r`ncd /d `"%~dp0`"`r`nmain.exe %*`r`nexit /b %errorlevel%`r`n", [Text.Encoding]::ASCII)

    Copy-Item -LiteralPath "$projectRoot/scripts/Start-WebUI.cmd" -Destination "$stage/Start-WebUI.cmd"
    Copy-Item -LiteralPath "$projectRoot/scripts/Stop-WebUI.cmd" -Destination "$stage/Stop-WebUI.cmd"

    Compress-Archive -Path "$stage/*" -DestinationPath $archive -CompressionLevel Optimal

    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$archive.sha256", "$hash  $archiveName`n", [Text.Encoding]::ASCII)

    Write-Output $reportedVersion
    Write-Output "Archive: $archive"
    Write-Output "SHA256: $hash"
}
finally {
    Pop-Location

    foreach ($key in $savedEnv.Keys) {
        [Environment]::SetEnvironmentVariable($key, $savedEnv[$key], 'Process')
    }
}
