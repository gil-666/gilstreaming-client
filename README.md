# GilStreaming

GilStreaming is a private, coordinator-driven game streaming client derived
from [Moonlight Qt](https://github.com/moonlight-stream/moonlight-qt). It is
intended to connect users only to Windows gaming VMs assigned from a private
pool. Sunshine provides the GameStream-compatible host inside each VM.

The first deployment target is one GPU-P enabled Windows host with a Sunshine VM
at `192.168.1.21`. Each VM represents one streaming slot. The coordinator keeps
the pool as a list so more VMs can be added later without changing the client.

## GilStreaming architecture

GilStreaming does not discover hosts or select a VM itself. A small coordinator
owns the pool and grants a time-limited lease for one available VM:

1. The client authenticates to the coordinator.
2. The client requests a streaming session.
3. The coordinator atomically reserves an available, healthy VM.
4. The coordinator returns only the assigned Sunshine endpoint.
5. The client pairs (when required), connects, and renews the lease.
6. The client releases the lease when the session ends. Expired leases are
   reclaimed automatically.

See [docs/architecture.md](docs/architecture.md) and
[docs/coordinator-api.md](docs/coordinator-api.md) for the initial design.

For public streaming, the coordinator endpoint is
`https://gilstreaming.gilservers.com`. The desktop first probes the assigned
Sunshine public endpoint and uses the native connection when reachable. On a
restrictive network it automatically carries TCP through authenticated
WebSocket channels on that HTTPS hostname and native UDP through Cloudflare
Realtime TURN. The older WebSocket UDP path remains a last-resort fallback.
Private VM addresses, lease routing, permanent TURN credentials, and the
Sunshine Web UI remain coordinator-only. The LAN coordinator setting continues
to connect directly for local testing.

## Portable build and run scripts

The scripts resolve all paths from the repository, so they can be invoked from
any working directory after cloning or copying the complete repository.

Windows PowerShell:

```powershell
# Build and run the coordinator
.\scripts\run-coordinator.ps1

# Build the debug client
.\scripts\build-client.ps1

# Run the debug client and follow its log
.\scripts\run-client-debug.ps1

# Build the release client
.\scripts\build-client.ps1 -Configuration Release
```

Linux:

```bash
# Build and run the coordinator
bash scripts/run-coordinator.sh

# Build the debug client
bash scripts/build-client.sh

# Run the debug client and follow its log
bash scripts/run-client-debug.sh

# Build the release client
bash scripts/build-client.sh release
```

The coordinator and client scripts require Go 1.22 or newer. The build scripts initialize
Git submodules automatically. Windows also downloads the prebuilt client
dependencies when missing; Qt MSVC and Visual Studio Build Tools must already be
installed. Linux requires the Qt and multimedia development packages listed
below. Client build scripts only compile; the debug launchers run the existing
debug binary and follow its log.

## Continuous builds

Every push runs `.github/workflows/build.yml`. The workflow tests and builds the
coordinator for Windows and Linux, builds Debug and Release clients for both
platforms, and uploads commit-specific artifacts for 14 days. Download them from
the pushed commit's run under the repository's **Actions** tab. It can also be
started manually with **Run workflow**. Coordinator artifacts include their
example configuration and platform-specific start script; private `.env` and
`config.json` files are deliberately not embedded in public builds.

After every fully successful build of `master`, the workflow also publishes a
rolling `continuous` prerelease containing `GilStreamingSetup-x64.exe`. Release
builds use a monotonically increasing `major.minor.run` version, so running a
newer installer upgrades the existing installation. Installed Windows clients
check the release's `update.json` manifest at startup and show an update button
when that build is newer.

## Current status

- Upstream Moonlight Qt source is imported on the `gilstreaming` branch.
- The application identity has been separated from Moonlight so settings and
  paired-host state are stored under GilStreaming.
- A Go coordinator provides brokered GILid authentication, Sunshine mDNS IP
  discovery, persistent atomic VM leases, WSS TCP relay, and short-lived
  Cloudflare TURN credentials for native UDP fallback.
- The desktop has a GILid login screen, a debug-only login skip, coordinator VM
  assignment, lease heartbeats, and coordinator-only host entry.
- Automatic Sunshine pairing is implemented; VM health/cleanup is the next
  vertical slice.

## Upstream Moonlight features

## Features
 - Hardware accelerated video decoding on Windows, Mac, and Linux
 - H.264, HEVC, and AV1 codec support (AV1 requires Sunshine and a supported host GPU)
 - YUV 4:4:4 support (Sunshine only)
 - HDR streaming support
 - 7.1 surround sound audio support
 - 10-point multitouch support (Sunshine only)
 - Gamepad support with force feedback and motion controls for up to 16 players
 - Support for both pointer capture (for games) and direct mouse control (for remote desktop)
 - Support for passing system-wide keyboard shortcuts like Alt+Tab to the host
 
## Downloads
- [Windows, macOS, and Steam Link](https://github.com/moonlight-stream/moonlight-qt/releases)
- [Snap (for Ubuntu-based Linux distros)](https://snapcraft.io/moonlight)
- [Flatpak (for other Linux distros)](https://flathub.org/apps/details/com.moonlight_stream.Moonlight)
- [AppImage](https://github.com/moonlight-stream/moonlight-qt/releases)
- [Raspberry Pi 4 and 5](https://github.com/moonlight-stream/moonlight-docs/wiki/Installing-Moonlight-Qt-on-Raspberry-Pi-4)
- [Generic ARM 32-bit and 64-bit Debian packages](https://github.com/moonlight-stream/moonlight-docs/wiki/Installing-Moonlight-Qt-on-ARM%E2%80%90based-Single-Board-Computers) (not for Raspberry Pi)
- [Experimental RISC-V Debian packages](https://github.com/moonlight-stream/moonlight-docs/wiki/Installing-Moonlight-Qt-on-RISC%E2%80%90V-Single-Board-Computers)
- [NVIDIA Jetson and Nintendo Switch (Ubuntu L4T)](https://github.com/moonlight-stream/moonlight-docs/wiki/Installing-Moonlight-Qt-on-Linux4Tegra-(L4T)-Ubuntu)

### Nightly Builds
- [Downloads](https://nightly.link/moonlight-stream/moonlight-qt/workflows/build/master)

#### Special Thanks

[![Hosted By: Cloudsmith](https://img.shields.io/badge/OSS%20hosting%20by-cloudsmith-blue?logo=cloudsmith&style=flat-square)](https://cloudsmith.com)

Hosting for Moonlight's Debian and L4T package repositories is graciously provided for free by [Cloudsmith](https://cloudsmith.com).

## Building

### Windows Build Requirements
* Qt 6.11 SDK or later (earlier versions may work but are not officially supported)
* [Visual Studio 2026](https://visualstudio.microsoft.com/downloads/) (Community edition is fine)
* Select **MSVC** option during Qt installation. MinGW is not supported.
* [7-Zip](https://www.7-zip.org/) (only if building installers for non-development PCs)
* Graphics Tools (only if running debug builds)
  * Install "Graphics Tools" in the Optional Features page of the Windows Settings app.
  * Alternatively, run `dism /online /add-capability /capabilityname:Tools.Graphics.DirectX~~~~0.0.1.0` and reboot.

### macOS Build Requirements
* Qt 6.11 SDK or later (earlier versions may work but are not officially supported)
* Xcode 15 or later (earlier versions may work but are not officially supported)
* [create-dmg](https://github.com/sindresorhus/create-dmg) (only if building DMGs for use on non-development Macs)

### Linux/Unix Build Requirements
* Qt 6 is recommended, but Qt 5.12 or later is also supported (replace `qmake6` with `qmake` when using Qt 5).
* GCC or Clang
* FFmpeg 4.0 or later
* Install the required packages:
  * Debian/Ubuntu:
    * Base Requirements: `libegl1-mesa-dev libgl1-mesa-dev libopus-dev libsdl2-dev libsdl2-ttf-dev libssl-dev libavcodec-dev libavformat-dev libswscale-dev libva-dev libvdpau-dev libxkbcommon-dev wayland-protocols libdrm-dev`
    * Qt 6 (Recommended): `qt6-base-dev qt6-declarative-dev libqt6svg6-dev qt6-websockets-dev qt6-wayland qml6-module-qtquick-controls qml6-module-qtquick-templates qml6-module-qtquick-layouts qml6-module-qtqml-workerscript qml6-module-qtquick-window qml6-module-qtquick`
    * Qt 5: `qtbase5-dev qt5-qmake qtdeclarative5-dev qtquickcontrols2-5-dev qml-module-qtquick-controls2 qml-module-qtquick-layouts qml-module-qtquick-window2 qml-module-qtquick2 qtwayland5`
  * RedHat/Fedora (RPM Fusion repo required):
    * Base Requirements: `openssl-devel SDL2-devel SDL2_ttf-devel ffmpeg-devel libva-devel libvdpau-devel opus-devel pulseaudio-libs-devel alsa-lib-devel libdrm-devel`
    * Qt 6 (Recommended): `qt6-qtsvg-devel qt6-qtdeclarative-devel`
    * Qt 5: `qt5-qtsvg-devel qt5-qtquickcontrols2-devel`
* Building the Vulkan renderer requires a `libplacebo-dev`/`libplacebo-devel` version of at least v7.349.0 and FFmpeg 6.1 or later.

### Steam Link Build Requirements
* [Steam Link SDK](https://github.com/ValveSoftware/steamlink-sdk) cloned on your build system
* STEAMLINK_SDK_PATH environment variable set to the Steam Link SDK path

**Steam Link Hardware Limitations**  
Moonlight builds for Steam Link are subject to hardware limitations of the Steam Link device:
* Maximum resolution: **1080p (1920x1080)**
* Maximum framerate: **60 FPS**
* Maximum video bitrate: **40 Mbps**
* **HDR streaming is not supported** on the original hardware

### Docker containers
If you want to use Docker for building, look at [this repo](https://github.com/cgutman/moonlight-packaging) containing canonical containers
for different architectures, which handle building deps and extra linking for you.

### Build Setup Steps
1. Install the latest Qt SDK (and optionally, the Qt Creator IDE) from https://www.qt.io/download
    * You can install Qt via Homebrew on macOS, but you will need to use `brew install qt --with-debug` to be able to create debug builds of Moonlight.
    * You may also use your Linux distro's package manager for the Qt SDK as long as the packages are Qt 5.12 or later.
    * This step is not required for building on Steam Link, because the Steam Link SDK includes Qt 5.14.
2. Download submodules and dependencies
    * Run `git submodule update --init --recursive` from within `moonlight-qt/`.
    * On Windows and macOS, you must also run `setup-deps.ps1` (Windows) or `setup-deps.py` (macOS).
    * Perform these steps each time you pull new changes from the Git repository.
3. Open the project in Qt Creator or build from qmake on the command line.
    * To build a binary for use on non-development machines, use the scripts in the `scripts` folder.
        * For Windows builds, use `scripts\build-arch.bat` and `scripts\generate-bundle.bat`. Execute these scripts from the root of the repository within a Qt command prompt. Ensure  7-Zip binary directory is on your `%PATH%`.
        * For macOS builds, use `scripts/generate-dmg.sh`. Execute this script from the root of the repository and ensure Qt's `bin` folder is in your `$PATH`.
        * For Steam Link builds, run `scripts/build-steamlink-app.sh` from the root of the repository.
    * To build from the command line for development use on macOS or Linux, run `qmake6 moonlight-qt.pro` then `make debug` or `make release`.
        * The final binary will be placed in `app/moonlight`.
    * To create an embedded build for a single-purpose device, use `qmake6 "CONFIG+=embedded" moonlight-qt.pro` and build normally.
        * This build will lack windowed mode, Discord/Help links, and other features that don't make sense on an embedded device.
        * For platforms with poor GPU performance, add `"CONFIG+=gpuslow"` to prefer direct KMSDRM rendering over GL/Vulkan renderers. Direct KMSDRM rendering can use dedicated YUV/RGB conversion and scaling hardware rather than slower GPU shaders for these operations.

## Contribute
1. Fork us
2. Write code
3. Send Pull Requests

Check out our [website](https://moonlight-stream.org) for project links and information.
