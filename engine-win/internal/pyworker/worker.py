#!/usr/bin/env python3
"""AudioSmith optional Windows Python/PyTorch worker (protocol-v2 NDJSON).

Speaks protocol-v2 JSON lines over stdin/stdout (logs go to stderr only).
Auto-selects CUDA when available, else DirectML (torch_directml), else CPU.
Supports Qwen3-TTS (zh/en/ja) and Irodori-TTS v4.1 (ja).
"""

import gc
import json
import os
import struct
import sys
import time
import wave
from pathlib import Path


def log(msg: str) -> None:
    sys.stderr.write(f"[pyworker] {msg}\n")
    sys.stderr.flush()


def send(obj: dict) -> None:
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def send_result(req_id: str, result: dict) -> None:
    send({"id": req_id, "result": result})


def send_error(req_id: str, code: str, message: str, data: dict | None = None) -> None:
    err: dict = {"code": code, "message": message}
    if data is not None:
        err["data"] = data
    send({"id": req_id, "error": err})


def send_notify(method: str, params: dict) -> None:
    send({"method": method, "params": params})


def select_device() -> tuple[str, object]:
    override = os.environ.get("AUDIOSMITH_PY_DEVICE", "").strip().lower()
    try:
        import torch
    except ImportError:
        return "cpu", "cpu"

    if override == "cpu":
        return "cpu", "cpu"
    if override in ("", "cuda") and torch.cuda.is_available():
        return "cuda", "cuda"
    if override in ("", "dml", "directml"):
        try:
            import torch_directml  # type: ignore

            return "directml", torch_directml.device()
        except ImportError:
            pass
    return "cpu", "cpu"


def detect_family(model: str, path: str) -> str:
    combined = f"{model} {path}".lower()
    if "irodori" in combined:
        return "irodori_tts"
    if "qwen3" in combined and "tts" in combined:
        return "qwen3_tts"
    if "qwen3_tts" in combined:
        return "qwen3_tts"
    return "unknown"


def write_pcm16_wav(out_path: str, samples, sample_rate: int) -> tuple[int, float]:
    import numpy as np

    arr = np.asarray(samples, dtype=np.float32).reshape(-1)
    arr = np.clip(arr, -1.0, 1.0)
    pcm = (arr * 32767.0).astype("<i2")

    dest = Path(out_path)
    dest.parent.mkdir(parents=True, exist_ok=True)
    tmp = dest.with_suffix(dest.suffix + ".tmp")
    with wave.open(str(tmp), "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(int(sample_rate))
        wf.writeframes(pcm.tobytes())
    os.replace(tmp, dest)
    frames = int(pcm.shape[0])
    return frames, float(frames) / float(sample_rate) if sample_rate > 0 else 0.0


class Worker:
    def __init__(self) -> None:
        self.device_name, self.device = select_device()
        self.models: dict[str, dict] = {}

    def handle(self, req: dict) -> bool:
        req_id = req.get("id", "")
        method = req.get("method", "")
        params = req.get("params") or {}

        if method == "engine.hello":
            send_result(
                req_id,
                {
                    "protocol": 2,
                    "role": "pyworker",
                    "version": "0.1.0",
                    "device": self.device_name,
                    "families": ["qwen3_tts", "irodori_tts"],
                },
            )
            return True

        if method == "engine.shutdown":
            self.models.clear()
            send_result(req_id, {})
            return False

        if method == "models.load":
            self.handle_load(req_id, params)
            return True

        if method == "models.unload":
            self.handle_unload(req_id, params)
            return True

        if method == "tts.synthesize":
            self.handle_synthesize(req_id, params)
            return True

        if method == "tts.encodeVoice":
            self.handle_encode_voice(req_id, params)
            return True

        send_error(req_id, "method_not_found", f"unknown method: {method}")
        return True

    def handle_load(self, req_id: str, params: dict) -> None:
        model_id = params.get("model", "")
        model_path = params.get("path", "") or model_id
        task = params.get("task", "tts") or "tts"
        family = detect_family(model_id, model_path)
        t0 = time.monotonic()

        try:
            import torch
        except ImportError:
            send_error(
                req_id,
                "unsupported",
                "PyTorch is not installed in the optional Python worker (install torch, qwen-tts / irodori-tts)",
                {"backend": "python", "provider": self.device_name, "capability": "models.load"},
            )
            return

        if family == "qwen3_tts":
            try:
                from qwen_tts import Qwen3TTSModel  # type: ignore
            except ImportError:
                send_error(
                    req_id,
                    "unsupported",
                    "qwen-tts is not installed in the optional Python worker (pip install qwen-tts)",
                    {"backend": "python", "provider": self.device_name, "capability": "qwen3_tts"},
                )
                return
            dtype = torch.bfloat16 if self.device_name == "cuda" else torch.float32
            inst = Qwen3TTSModel.from_pretrained(model_path, device_map=self.device, dtype=dtype)
            self.models[model_id] = {"family": family, "instance": inst, "sampleRate": 24000}
            send_result(
                req_id,
                {
                    "model": model_id,
                    "task": task,
                    "sampleRate": 24000,
                    "seconds": round(time.monotonic() - t0, 4),
                    "bytes": 0,
                    "languages": ["zh", "en", "ja", "ko", "de", "fr", "es", "it", "pt", "ru"],
                },
            )
            return

        if family == "irodori_tts":
            try:
                from irodori_tts import IrodoriTTS  # type: ignore
            except ImportError:
                send_error(
                    req_id,
                    "unsupported",
                    "irodori-tts is not installed in the optional Python worker (pip install irodori-tts)",
                    {"backend": "python", "provider": self.device_name, "capability": "irodori_tts"},
                )
                return
            inst = IrodoriTTS.from_pretrained(model_path, device=self.device)
            self.models[model_id] = {"family": family, "instance": inst, "sampleRate": 48000}
            send_result(
                req_id,
                {
                    "model": model_id,
                    "task": task,
                    "sampleRate": 48000,
                    "seconds": round(time.monotonic() - t0, 4),
                    "bytes": 0,
                    "languages": ["ja"],
                },
            )
            return

        send_error(
            req_id,
            "unsupported",
            f"python worker does not support model family for {model_id!r}",
            {"backend": "python", "provider": self.device_name, "capability": "models.load"},
        )

    def handle_unload(self, req_id: str, params: dict) -> None:
        model_id = params.get("model", "")
        unloaded = []
        if model_id:
            if model_id in self.models:
                del self.models[model_id]
                unloaded.append(model_id)
        else:
            unloaded = sorted(self.models.keys())
            self.models.clear()
        gc.collect()
        try:
            import torch

            if torch.cuda.is_available():
                torch.cuda.empty_cache()
        except Exception:
            pass
        send_result(req_id, {"unloaded": unloaded})

    def handle_synthesize(self, req_id: str, params: dict) -> None:
        model_id = params.get("model", "")
        entry = self.models.get(model_id)
        if entry is None:
            send_error(req_id, "not_loaded", f"model {model_id!r} is not loaded")
            return

        job_id = params.get("job", req_id)
        text = params.get("text", "")
        out_spec = params.get("output") or {}
        out_path = out_spec.get("path", "")
        if not out_path:
            send_error(req_id, "bad_request", "output.path is required")
            return

        send_notify("job.progress", {"job": job_id, "stage": "generating", "fraction": 0.1})
        t0 = time.monotonic()
        family = entry["family"]
        inst = entry["instance"]
        sr = int(entry["sampleRate"])

        try:
            if family == "qwen3_tts":
                voice = params.get("voice") or "Vivian"
                lang = params.get("language") or "Auto"
                instruct = params.get("instruct") or ""
                ref_audio = params.get("refAudio") or ""
                ref_text = params.get("refText") or ""
                if ref_audio:
                    wavs, sr = inst.generate_voice_clone(
                        text=text, language=lang, ref_audio=ref_audio, ref_text=ref_text
                    )
                else:
                    wavs, sr = inst.generate_custom_voice(
                        text=text, language=lang, speaker=voice, instruct=instruct
                    )
                samples = wavs[0] if isinstance(wavs, (list, tuple)) else wavs
            elif family == "irodori_tts":
                ref_audio = params.get("refAudio") or ""
                instruct = params.get("instruct") or ""
                kwargs = {"text": text}
                if ref_audio:
                    kwargs["ref_audio"] = ref_audio
                if instruct:
                    kwargs["voice_description"] = instruct
                if params.get("seed") is not None:
                    kwargs["seed"] = int(params["seed"])
                out = inst.generate(**kwargs)
                if isinstance(out, tuple) and len(out) == 2:
                    samples, sr = out
                else:
                    samples = out
            else:
                send_error(req_id, "unsupported", f"unsupported family {family}")
                return
        except Exception as exc:
            send_error(req_id, "internal", f"{family} synthesis failed: {exc}")
            return

        frames, audio_sec = write_pcm16_wav(out_path, samples, int(sr))
        elapsed = max(time.monotonic() - t0, 1e-6)
        rtf = audio_sec / elapsed if audio_sec > 0 else 0.0
        send_notify("job.progress", {"job": job_id, "stage": "done", "fraction": 1.0})
        send_result(
            req_id,
            {
                "output": out_path,
                "model": model_id,
                "sampleRate": int(sr),
                "channels": 1,
                "frames": frames,
                "audioSeconds": round(audio_sec, 4),
                "seconds": round(elapsed, 4),
                "rtf": round(rtf, 4),
            },
        )

    def handle_encode_voice(self, req_id: str, params: dict) -> None:
        model_id = params.get("model", "")
        prompt_path = params.get("promptPath", "")
        ref_audio = params.get("refAudio", "")
        ref_text = params.get("refText", "")
        if not prompt_path:
            send_error(req_id, "bad_request", "promptPath is required")
            return
        dest = Path(prompt_path)
        dest.parent.mkdir(parents=True, exist_ok=True)
        payload = json.dumps(
            {"model": model_id, "refAudio": ref_audio, "refText": ref_text},
            ensure_ascii=False,
        ).encode("utf-8")
        dest.write_bytes(payload)
        send_result(
            req_id,
            {
                "voicePrompt": prompt_path,
                "model": model_id,
                "format": "pyworker/1",
                "bytes": len(payload),
            },
        )


def main() -> int:
    worker = Worker()
    for raw in sys.stdin:
        line = raw.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError as exc:
            log(f"invalid json: {exc}")
            continue
        if not isinstance(req, dict):
            continue
        try:
            keep_going = worker.handle(req)
        except Exception as exc:
            req_id = req.get("id", "")
            if req_id:
                send_error(req_id, "internal", str(exc))
            log(f"unhandled error: {exc}")
            keep_going = True
        if not keep_going:
            break
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
