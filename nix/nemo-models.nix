{ fetchurl }:
# Revisions and hashes from NeMo-Speech.cpp v0.2.0's model index.
map
  (
    model:
    model
    // {
      file = fetchurl {
        url = "https://huggingface.co/${model.repo}/resolve/${model.revision}/${model.filename}";
        sha256 = model.hash;
      };
    }
  )
  [
    {
      repo = "nvidia/nemotron-speech-streaming-en-0.6b";
      revision = "ebe59e5a817142986528bbbee5dba8db7b38ed50";
      filename = "nemotron-speech-streaming-en-0.6b.q8_0.gguf";
      hash = "d9a01898d2a611c8764e23a1c2f45e70bbd5a425dc4de93692ac951dd603812d";
    }
    {
      repo = "nvidia/parakeet-tdt-0.6b-v3";
      revision = "541d1f99c6b0c3cd0b11a95167540bb8edefd82b";
      filename = "parakeet-tdt-0.6b-v3.q8_0.gguf";
      hash = "e3880d0aaaaf2c308ea2c35016b2b895c423eb3fda924c1b463d1c19b7f4d32e";
    }
    {
      repo = "nvidia/Nemotron-3-Diarization";
      revision = "f667ed73aee57d40cc39428eb768b4fd87a0a29e";
      filename = "Nemotron-3-Diarization.q8_0.gguf";
      hash = "08456d9e22cd9a323c0364d98375f3746d6e68507ebb705cd46438c534c7a3a1";
    }
  ]
