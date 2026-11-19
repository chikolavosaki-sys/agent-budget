# Research log

Newest first. One entry per research session.

Format:

```
## <YYYY-MM-DD> — <project studied>
- Read: <links actually read>
- Core mechanism (plain words):
- Ideas added: IDEA-<n>, IDEA-<n>
- Spike proposed: <one line> (accepted / not yet / rejected)
```

## 2026-11-19 — OpenAI chat completions API (+ Ollama compatibility)
- Read: https://github.com/openai/openai-openapi (`CompletionUsage`, `CreateChatCompletionStreamResponse`, `stream_options.include_usage`); https://docs.ollama.com/api/openai-compatibility
- Core mechanism (plain words): a normal answer carries `usage.prompt_tokens` / `usage.completion_tokens`. A streamed answer carries usage only if the request sets `stream_options.include_usage`; then every chunk has `"usage": null` except one extra chunk with empty `choices` just before `[DONE]`. If the stream is interrupted, that chunk may never arrive. Ollama serves the same API on port 11434 and lists `include_usage` as supported.
- Ideas added: IDEA-1 (done, D-006), IDEA-2 (token reservation for hard limits)
- Spike proposed: none — IDEA-1 was built directly.
