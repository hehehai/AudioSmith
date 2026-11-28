# AudioSmith Windows Engine (`engine-win`)

Windows hybrid engine implementing AudioSmith's `docs/protocol-v2.md` JSON-RPC over stdio (`--role gpu|listen|media`).

## Everyday / iGPU-First Design

Many Windows users run Intel/AMD integrated GPUs or everyday CPU PCs rather than high-end NVIDIA GPUs. `engine-win` is built so that:

- **Zero-CGO single Go binary** (`audiosmith-engine.exe`): cross-compiles cleanly to `windows/amd64` and `windows/arm64` with `CGO_ENABLED=0`, keeping the base binary small (~3–5 MB).
- **Hardware capability probe (`engine-win/hw`)**: enumerates DXGI adapters (`dxgi.dll`), system RAM (`GlobalMemoryStatusEx`), OS version (`RtlGetVersion`), and optional runtime DLLs (`nvcuda.dll`, `DirectML.dll`, `vulkan-1.dll`) without assuming NVIDIA. Classifies hosts into `cuda`, `directml`, `vulkan`, or `cpu` tiers with separate VRAM and WDDM shared-RAM budgets.
- **`media` role (`engine-win/media`)**:
  - Pure-Go WAV probe, trim, channel mixdown, and resampling (`internal/dsp/wav`, `internal/dsp/resample`) work out of the box even before FFmpeg is installed.
  - Full M4A/M4B/MP4/MOV encode, mux, thumbnail, and preview transcode via `internal/ffmpeg` with hardware H.264 encoder detection (`h264_qsv` / `h264_amf` / `h264_mf` / `h264_nvenc` → `libx264` / `mpeg4` fallback).
  - Clean `WatermarkBackend` interface for Phase 1.5 AudioSeal ONNX Runtime CPU execution.
- **`listen` role (`engine-win/listen`)**:
  - Pure-Go, zero-CGO Win32 COM/WASAPI device enumeration (`capture.devices`) and shared-mode audio capture (`capture.start` / `capture.stop`), 20 Hz `capture.level` dBFS metering, WAV output via `internal/dsp/wav`, and an `AECProcessor` hook for Phase 1.5 WebRTC APM.
- **`gpu` role (`engine-win/gpu`)**:
  - Pluggable iGPU-first backend router:
    1. **Native ONNX Runtime (`engine-win/internal/onnx`)**: zero-CGO `syscall.NewLazyDLL` loader for `onnxruntime.dll` (`OrtGetApiBase` → `OrtApi` vtable), DirectML EP on Intel/AMD/Qualcomm iGPU/dGPU with automatic CPU EP fallback. Ships real **Silero VAD** (v5 & v4, `vad.detect`) and **Kokoro 82M ONNX** (`tts.synthesize`, 24 kHz WAV + streaming `tts.chunk`, built-in pure-Go English G2P without espeak).
    2. **`llama.cpp` (`engine-win/internal/llamacpp`)**: drives managed `llama-server.exe` / `llama-cli.exe` subprocesses or `AUDIOSMITH_LLAMA_URL` (`Vulkan` on Intel/AMD iGPU, `CUDA` on NVIDIA, `CPU` fallback; streaming `llm.token` + JSON Schema / GBNF).
    3. **`whisper.cpp` (`engine-win/internal/llamacpp`)**: drives `whisper-cli.exe` / `whisper-server.exe` for `stt.transcribe` with automatic 16 kHz mono WAV resampling.
    4. **Optional on-demand Python worker bridge** for PyTorch-only frontier models (never required at install time).

Runtime binaries/DLLs are discovered from environment overrides (`AUDIOSMITH_ONNXRUNTIME`, `AUDIOSMITH_LLAMA_SERVER`, `AUDIOSMITH_LLAMA_CLI`, `AUDIOSMITH_WHISPER_CLI`, `AUDIOSMITH_LLAMA_URL`, `AUDIOSMITH_WHISPER_URL`), beside `audiosmith-engine.exe`, or under `%LOCALAPPDATA%\ActNow\runtimes\{onnx,llama,whisper}\`.

## Building

```bash
scripts/build-engine-win.sh all
```

Quick CLI hardware probe on Windows:

```powershell
.\audiosmith-engine.exe --probe-hw
```
