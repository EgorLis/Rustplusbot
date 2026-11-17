$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$toolsDir = Join-Path $projectRoot '.local/tools'
$compiler = Join-Path $toolsDir 'protoc-36.2/bin/protoc.exe'
$plugin = Join-Path $toolsDir 'protoc-gen-go.exe'

if (!(Test-Path -LiteralPath $compiler) -or !(Test-Path -LiteralPath $plugin)) {
    throw 'Missing Protobuf tools. Follow internal/proto/README.md to install the pinned versions.'
}

if ((& $compiler --version) -ne 'libprotoc 36.2') {
    throw 'Expected protoc 36.2.'
}

if ((& $plugin --version) -notmatch 'v1\.36\.12$') {
    throw 'Expected protoc-gen-go 1.36.12.'
}

& $compiler `
    "--plugin=protoc-gen-go=$plugin" `
    "--proto_path=$projectRoot/internal/proto" `
    "--go_out=$projectRoot/internal/rpclient" `
    '--go_opt=paths=source_relative' `
    "$projectRoot/internal/proto/rustplus.proto"

if ($LASTEXITCODE -ne 0) {
    throw "Protobuf generation failed with exit code $LASTEXITCODE."
}
