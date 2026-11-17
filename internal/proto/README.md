# Rust+ schema

Source: [HandyS11/RustPlusApi, RustPlusContracts.proto](https://github.com/HandyS11/RustPlusApi/blob/45a0f6f1decb0c83f052162deec6e473e767836e/src/RustPlusApi/Protobuf/RustPlusContracts.proto).

Checked on 2026-11-17. This is a maintained community schema, not an official Facepunch protocol specification. The upstream revision is pinned so updates are reviewable.

The local schema changes only the package name to `rpclient` and adds `go_package`. Field numbers, types and names match that revision. New fields include 64-bit entity and marker IDs, team map notes, travelling vendors, camera metadata, clan scores and `kick_from_team`. Adding a wire message does not enable a new bot command.

Incoming messages use `AllowPartial`: the live companion socket omits some default-valued fields declared `required` upstream, including `queued_players`. Malformed binary messages are still rejected; startup additionally checks the response sequence, server error and server name.

## Generate

Pinned tools:

- Official [protoc 36.2 Windows archive](https://github.com/protocolbuffers/protobuf/releases/download/v36.2/protoc-36.2-win64.zip), extracted into `.local/tools/protoc-36.2`.
- `protoc-gen-go` 1.36.12, matching the Go Protobuf runtime.

Install the Go plugin from the project root:

```powershell
$env:GOBIN = Join-Path $PWD '.local/tools'
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
```

Then run:

```powershell
go generate ./internal/proto
go test ./...
```

The output is `internal/rpclient/rustplus.pb.go`. Do not edit that generated file manually.

The generator runs the checked-in PowerShell script with a process-local execution policy. It does not change the machine's execution policy.

For an explicit read-only live check using your private config:

```powershell
$env:RUSTPLUS_LIVE_CONFIG = Join-Path $PWD '.local/conf/rpconfig.json'
go test ./internal/rpclient -run '^TestLiveCompanionConnection$' -count=1 -v -timeout 60s
```

That test reads server/team/time information, waits for a heartbeat, and reconnects after deliberately closing its own socket. It does not change devices or send chat messages.
