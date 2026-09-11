# GilStreaming coordinator prototype

This Go service is the pool authority for the VM pool. The example configuration
starts with one Sunshine VM and can be extended by adding entries to the `vms`
array. It
supports authenticated lease creation, idempotent recovery, heartbeats, release,
expiry, automatic Sunshine pairing, and crash-safe JSON state persistence.

GILid uses a confidential OAuth authorization-code flow. The coordinator owns
the callback, exchanges the code using the client secret, loads the GILid
profile, and returns a separate short-lived GilStreaming session to the desktop
client. The GILid secret must exist only in the coordinator environment.

The service does not yet perform VM health checks or cleanup.
Keep it on a private network until TLS is terminated by a trusted reverse proxy.

```powershell
Copy-Item config.example.json config.json
Copy-Item .env.example .env
# Fill in .env. It is loaded automatically and ignored by Git.
go run . -config config.json
```

To verify discovery without starting another listener:

```powershell
go run . -config config.json -discover-only
```

Automatic pairing uses each VM's Sunshine Web UI API. Put the shared Web UI
username and password in `SUNSHINE_USERNAME` and `SUNSHINE_PASSWORD`. These
credentials stay on the coordinator and are never sent to a client. Sunshine's
self-signed Web UI certificate is accepted only for configured private VM
endpoints.

Before granting a lease, the coordinator browses Sunshine's
`_nvstream._tcp.local` mDNS service. Set `discoveryName` to the VM's unique
Windows/Sunshine hostname (for example, `v1`). The coordinator derives the
private stream address, Sunshine base port, and Web UI URL from that record. If
the VM cannot be discovered, it is not assigned.
Give each cloned VM a unique hostname (`v1`, `v2`, and so on); the shared
Sunshine username and password do not need to change.

Variables already present in the process environment take precedence over the
file. Use `-env-file path` to select a different file. A missing `.env` is
allowed for production deployments that inject secrets through a service
manager.

For a local debug client, point it at the development coordinator before launch:

```powershell
$env:GILSTREAMING_COORDINATOR_URL = "http://127.0.0.1:6766"
```

Production clients default to `https://gilstreaming.gilservers.com:6766`.

The private coordinator-side endpoints and Sunshine base port are populated by
mDNS. `publicAddress` is returned to clients with the discovered base port. For
this deployment it is `stream.gilservers.com`; Sunshine advertises and publishes
port `47989`. The DNS record must resolve directly to the router's public IP.
Do not publish Sunshine's Web UI port (`47990`).

Run tests with `go test ./...`.

GitHub Actions coordinator downloads include the executable, example files, and
a platform launcher. After extracting the artifact, copy `config.example.json`
to `config.json`, copy `.env.example` to `.env`, fill in the private values, and
run `start-coordinator.ps1` on Windows or `start-coordinator.sh` on Linux.

From the repository root, the portable launcher builds the coordinator into the
ignored `build/coordinator` directory and runs it with the local config and env
files:

```powershell
.\scripts\run-coordinator.ps1
```

```bash
bash scripts/run-coordinator.sh
```

`devAuthEnabled` exposes the login-skip endpoint used by debug client builds.
Set it to `false` anywhere except a controlled test environment. Release builds
do not show the skip button.
