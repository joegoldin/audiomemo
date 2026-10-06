# Handoff: local transcription research for audiomemo

## User request and scope

Investigate the current state of the art in local speech-to-text that can run in **less than 8 GB RAM**, ideally fast on:

- An M4 MacBook Pro (exact M4 variant and installed memory not confirmed).
- A Linux desktop with an AMD GPU (GPU model and CPU not confirmed).
- CPU alone where practical.

The goal is to replace the default ElevenLabs transcription in audiomemo while retaining **real-time preview**, followed by **higher-quality final transcription and speaker diarization**.

This is research, not authorization to implement, install models, upload recordings, or change defaults. The user specifically requested another web-search pass to establish SOTA, then requested this handoff so a new conversation can use a working search tool.

Working assumptions, not confirmed requirements: mostly English; 8 GB refers to the transcription workload rather than total installed system RAM. Do not block initial research on routine clarifications.

## Why research needs continuing

The first response was based on knowledge through August 2025 and recommended Whisper turbo/large-v3, Parakeet, and a separate diarizer. Subsequent live retrieval of current upstream documentation materially expanded the shortlist.

**A proper broad web search has still not happened.** Tool discovery through `searchTools("web")` and `searchTools("search")` returned `{}` even after the user fixed their search configuration. Direct HTTP retrieval of GitHub READMEs and Hugging Face model cards worked. Attempts at public search pages produced a Google redirect page, a DuckDuckGo CAPTCHA, and irrelevant Bing RSS results. Do not represent those as a successful SOTA search.

The primary-source work below is useful but biased toward projects already discovered. It is not an exhaustive survey, independent comparison, or verified ranking. The session date was October 6, 2026; recheck the date and release timelines in the new conversation.

## Evidence status

- Current upstream documentation and model cards were retrieved in this session.
- No ASR or diarization models were installed or benchmarked.
- No recordings were uploaded; no paid APIs were called.
- No measured peak RAM, latency, accuracy, or AMD performance on the user's machines exists.
- No application code was changed. This handoff is the only added project file.
- Published model/download size is **not** process peak memory. GPU/unified-memory allocations, loading peaks, caches, audio buffers, and concurrent pipelines matter.
- Report upstream benchmarks as upstream claims with hardware, dataset, quantization, and streaming mode attached.

## Current provisional recommendations

These are candidates to investigate, not settled conclusions:

1. **NeMo-Speech.cpp** with Nemotron streaming ASR, Parakeet final ASR, and its diarization support: promising common runtime for CPU, Metal, and Vulkan/AMD.
2. **FluidAudio**: promising Apple-native stack using Core ML/Neural Engine for streaming ASR, final ASR, and offline diarization.
3. **Moonshine Medium Streaming**: promising lightweight CPU-first live preview.
4. Quality challengers: **Qwen3-ASR**, **Cohere Transcribe**, and quantized **Voxtral Realtime**.
5. Joint transcription/speaker-attribution challenger: **VibeVoice-ASR-Streaming-1.5B**.
6. **whisper.cpp with turbo/large-v3** remains a mature portable baseline, but not necessarily the best current streaming choice.

## Sources already retrieved and findings to verify

### NeMo-Speech.cpp / Nemotron

- https://github.com/NVIDIA/NeMo-Speech.cpp
- https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/BENCHMARK.md
- https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/build.md
- https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/cli.md
- https://huggingface.co/nvidia/nemotron-speech-streaming-en-0.6b
- https://huggingface.co/nvidia/nemotron-3.5-asr-streaming-0.6b

Retrieved README describes an official native ggml-based runtime with CPU, Metal, Vulkan, and CUDA backends; native SDK, CLI, and local HTTP/WebSocket serving. Listed models include Nemotron streaming 0.6B, Parakeet TDT v3, and Sortformer/Nemotron diarization.

Nemotron 3.5 card describes 40 language-locales, but only 19 as transcription-ready. Cache-aware streaming chunk sizes include 80, 160, 320, 560, and 1,120 ms. Runtime/export support may expose a different subset; distinguish upstream capability from a particular port.

Published CPU benchmark: English Nemotron 0.6B Q8, Intel i7-11700K, eight threads, 100-utterance/13.3-minute LibriSpeech subset. README reports approximately 6x real time at 160 ms chunks; benchmark page reports 19.2x at 1.12 s. These are not end-to-end caption latency measurements, and not AMD GPU results. Benchmark host had 128 GB installed RAM, which does not establish its workload memory requirement.

CLI docs say diarization V2 supports four speakers, V3 eight, and JSON includes word timestamps/speaker IDs. Live labels can change; V2 confirmation can take up to 10 seconds. Verify current model versions and performance before recommending this as a replacement.

### FluidAudio / Parakeet derivatives

- https://github.com/FluidInference/FluidAudio
- https://github.com/FluidInference/FluidAudio/blob/main/Documentation/Models.md
- https://github.com/FluidInference/FluidAudio/blob/main/Documentation/Benchmarks.md
- https://github.com/FluidInference/FluidAudio/blob/main/Documentation/ASR/ParakeetUltra.md
- https://huggingface.co/FluidInference/parakeet-tdt-0.6b-v3-coreml
- https://huggingface.co/FluidInference/parakeet-realtime-eou-120m-coreml
- https://huggingface.co/FluidInference/speaker-diarization-coreml

Swift/Core ML SDK targeting the Apple Neural Engine. Catalog includes Parakeet EOU 120M streaming, Nemotron 0.6B streaming, Parakeet TDT v3/Ultra/Redux, Phonon-2, Cohere Transcribe, and offline/online diarization.

Parakeet EOU offers 160/320/1,280 ms exports. TDT v3/Ultra use windowed batch processing, not the same genuine cache-aware streaming architecture.

FluidAudio recommends **Parakeet Ultra**, a moondream post-training of v3, for new integrations. Its paired evaluation reports WER v3 -> Ultra: LibriSpeech clean 2.27 -> 2.13%, other 4.12 -> 3.81%, FLEURS 24-language mean 14.81 -> 11.67%, at roughly similar speed. Ultra download ~630 MB; this is not peak memory. Some published paired benchmarks use M5 Pro, not the user's M4. Do not mix their results with M4 tables.

**Phonon-2** is an English-only quantization-aware v3 derivative. FluidAudio's catalog reports smaller artifacts/faster ANE processing but worse English LibriSpeech WER than v3/Ultra. Its Core ML implementation is distinct from an MLX implementation. Investigate upstream independently; do not conclude it is best because it is newer or Mac-native.

Diarization documentation describes Community-1-derived offline clustering. Benchmarks acknowledge conversion/default-setting accuracy trade-offs and substantially less robust online clustering. Documentation has some inconsistencies about which online pipeline derives from 3.1 versus Community-1; check pinned versions rather than assuming all paths are equivalent.

Licensing needs per-model checking. README's broad permissive-license language is not sufficient: Parakeet v3 model metadata lists CC-BY-4.0; EOU/Nemotron use NVIDIA Open Model License; supported Community-1 Core ML artifacts have scoped CC-BY-4.0 attribution/provenance. One Parakeet Core ML card had conflicting metadata/body license statements.

### Moonshine

- https://github.com/moonshine-ai/moonshine
- https://github.com/moonshine-ai/moonshine/blob/main/docs/models/available-models.md
- https://github.com/moonshine-ai/moonshine/blob/main/docs/models/accuracy.md

Current English streaming sizes: Tiny 34M, Small 123M, Medium 245M. Published eight-dataset average WER: 12.00%, 7.84%, 6.65%, respectively. **These headline scores use floating-point reference models.** Library ships 8-bit ONNX Runtime `.ort` models; docs separately report quantization accuracy. Whole-utterance/VAD-disabled accuracy is not equivalent to live-streaming pipeline accuracy.

Current docs say English streaming models are MIT; some legacy non-English non-streaming models retain a noncommercial Community license. Newer streaming lineup materially supersedes the original response's focus on old tiny/base models.

### Qwen3-ASR

- https://huggingface.co/Qwen/Qwen3-ASR-0.6B
- https://github.com/Blaizzy/mlx-audio

0.6B/1.7B, 30 languages plus 22 Chinese dialects; Apache-2.0. MLX-Audio lists quantized support. Official package's streaming path is vLLM-oriented; word timestamps require separate Qwen3-ForcedAligner-0.6B. Do not assume Transformers, MLX, and vLLM have identical streaming/timestamp support.

### Cohere Transcribe

- https://huggingface.co/CohereLabs/cohere-transcribe-03-2026
- https://cohere.com/blog/transcribe

2B, 14 languages, Apache-2.0. Card explicitly says **no timestamps or speaker diarization**. FluidAudio has a Core ML port with a 35-second per-call cap and explicit language prompt. Candidate for final-text quality, but alignment adds complexity. A raw card URL returned 401; the public `resolve/main/README.md` URL worked without credentials.

### Voxtral Realtime

- https://huggingface.co/mistralai/Voxtral-Mini-4B-Realtime-2602
- https://github.com/awni/voxmlx
- https://github.com/Blaizzy/mlx-audio

Native streaming, 13 languages, configurable delay, Apache-2.0. Official BF16/vLLM deployment calls for >=16 GB GPU memory, so not directly within the user's budget. Quantized MLX variants exist, including 4-bit. Actual total memory and live throughput need evidence. Do not conflate model delay settings with end-to-end latency. Card has stale/conflicting statements about framework support; verify runtime docs.

### VibeVoice joint transcription/diarization

- https://huggingface.co/microsoft/VibeVoice-ASR-Streaming-1.5B
- https://github.com/Blaizzy/mlx-audio/blob/main/mlx_audio/stt/models/vibevoice_asr/README.md
- https://arxiv.org/abs/2609.02812

Streaming speaker-attributed transcription, hotwords, 10 languages, MIT. This invalidates any blanket assertion that all local ASR requires a separate diarizer.

MLX docs report ~5.6 GB upstream BF16 weights for the model named 1.5B. Do not calculate total model footprint from the name alone. Streaming processes 2.933 s new audio plus 0.533 s lookahead, retains LM KV cache, and emits speaker/content rather than the long-form model's speaker/timestamps/content. Quantized memory, long-session behavior, and latency need validation.

### Pyannote / Whisper baselines

- https://github.com/pyannote/pyannote-audio
- https://huggingface.co/pyannote/speaker-diarization-community-1
- https://github.com/ggml-org/whisper.cpp

Community-1 is the current open local pyannote baseline found; generally improves on legacy 3.1 in the upstream table. Precision-2's default example is a hosted premium service, not a free local replacement. Community-1 requires accepting download conditions. Its HF raw card returned 401; the public GitHub README provided the documentation. Do not attempt access-control workarounds.

whisper.cpp documents Metal, Vulkan, and HIP/ROCm. Its memory table lists approximately 3.9 GB for the unquantized large model, with lower requirements under quantization. That is a runtime-specific upstream figure, not proof that ASR plus diarization stays below 8 GB.

## User-supplied search results

Immediately before requesting this handoff, the user pasted a search summary naming Whisper, NVIDIA Parakeet, and a Honoz article about Phonon-2 through MLX. The summary itself explicitly cautioned that its sources did not establish a winner or provide comparative benchmarks, measured speeds, hardware requirements, or verified diarization support.

Sources supplied by the user:

- https://picovoice.ai/docs/glossary/
- https://honoz.com/whisper-has-a-mac-problem-phonon-2-lands-parakeet-asr-on-apple-silicon/

These two pages were **not fetched or independently evaluated** in this session. Follow the Phonon-2 lead to its original model card, paper, and runtime benchmarks rather than relying on the article alone.

## audiomemo integration context

Read-only inspection established:

- `internal/transcribe/stream.go:18`: current Streamer is explicitly an ElevenLabs WebSocket session.
- `cmd/record.go:192–198`: live streamer constructed when transcription is requested and ElevenLabs API key exists; no selectable local preview backend here.
- `cmd/record.go:201–209`: recorder enables LivePCM when a streamer exists.
- `cmd/record.go:217–221`: PCM reader is passed to streamer.
- `cmd/record.go:259–264`: successful live TUI transcription skips post-recording batch transcription.
- `internal/transcribe/whisper.go`: existing subprocess integration supports whisper.cpp, Python Whisper, WhisperX, and FFmpeg Whisper; diarization is supported only by the WhisperX variant in this adapter.

Thus preview-then-refine requires a behavior change, not just choosing another default ASR model. A local subprocess or localhost service is a plausible integration boundary, but no design or implementation has been authorized yet.

Repository has `.jj/`; use jj for any VCS writes. Do not commit or otherwise alter user changes without authorization. At handoff creation, `git status --short` was empty before adding this document.

## What the next research pass should resolve

Use the working web-search tool to broaden discovery, then verify primary sources and independent measurements:

1. Find current open/local ASR accuracy leaders and new models missing from this shortlist, constrained to realistic <8 GB inference rather than parameter count alone.
2. Separate **English accuracy**, multilingual accuracy, real streaming latency, final long-form quality, and diarization quality. There may be different winners.
3. Look for **independent M4 and AMD/CPU benchmarks**, including total peak memory, cold start, warm latency, real-time factor, and long-session stability.
4. Check the current Open ASR leaderboard and its methodology. Do not rank models using WER numbers from incompatible datasets, normalization, precision, or streaming modes.
5. Compare actual runtime availability/maturity: CPU/Metal/Vulkan/ROCm support, model export fidelity, timestamp support, streaming events, and licensing.
6. Investigate Phonon-2 upstream and compare against Parakeet Ultra/v3 rather than treating the Mac-native headline as an accuracy claim.
7. Assess diarization independently, including overlap and speaker-count limits. Provisional live speaker labels and final offline labels can use different methods.
8. End with a short ranked evaluation shortlist for this user, explicit confidence and gaps, and which evidence would determine whether it really replaces ElevenLabs.

A plausible workflow remains: small/genuine-streaming preview -> final ASR over saved original audio -> offline diarization/alignment, with stages unloaded/sequenced to bound memory. But keep joint models and one-runtime solutions in consideration; do not force the evidence into the earlier architecture.
