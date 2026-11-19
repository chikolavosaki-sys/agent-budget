package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// prepareRequest looks at a request body before it is forwarded.
//
// If the body is a JSON object, it is re-encoded so that the provider receives
// exactly what we inspected (for example, a duplicated "stream" key cannot be
// read one way by us and another way by the provider). If the request asks for
// a streamed answer ("stream": true), it also sets
// stream_options.include_usage = true, so the provider sends a final chunk with
// the token usage of the whole request.
//
// Bodies that are not a JSON object are returned unchanged.
func prepareRequest(body []byte) (out []byte, stream bool, err error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil || obj == nil {
		return body, false, nil
	}

	if raw, ok := obj["stream"]; ok {
		_ = json.Unmarshal(raw, &stream) // not a bool → not a stream
	}
	if stream {
		opts := map[string]json.RawMessage{}
		if raw, ok := obj["stream_options"]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &opts); err != nil {
				return nil, true, fmt.Errorf("stream_options must be a JSON object: %w", err)
			}
		}
		opts["include_usage"] = json.RawMessage("true")
		if obj["stream_options"], err = marshalNoEscape(opts); err != nil {
			return nil, true, err
		}
	}

	out, err = marshalNoEscape(obj)
	return out, stream, err
}

// marshalNoEscape is json.Marshal without turning <, > and & into < etc.,
// so prompts keep their original characters.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// maxSSELine is the longest Server-Sent Events line we keep in memory while
// looking for usage. Longer lines are passed through but not inspected.
const maxSSELine = 1 << 20 // 1 MiB

// usageStreamReader passes a Server-Sent Events stream through unchanged while
// watching it for a chunk that carries "usage". When the stream ends (end of
// file, error, or Close) it calls onDone exactly once with the last usage it
// saw, or found=false if there was none.
//
// It is meant to be used by one goroutine at a time, which is how
// httputil.ReverseProxy uses a response body (it reads, then closes).
type usageStreamReader struct {
	src      io.ReadCloser
	onDone   func(u Usage, found bool)
	pending  []byte // the start of a line whose '\n' has not arrived yet
	skipping bool   // current line is longer than maxSSELine: ignore it
	last     Usage
	found    bool
	once     sync.Once
}

func newUsageStreamReader(src io.ReadCloser, onDone func(Usage, bool)) *usageStreamReader {
	return &usageStreamReader{src: src, onDone: onDone}
}

// Read gives the caller the bytes immediately — we only peek at them.
func (s *usageStreamReader) Read(p []byte) (int, error) {
	n, err := s.src.Read(p)
	if n > 0 {
		s.scan(p[:n])
	}
	if err != nil { // io.EOF or a broken connection: the stream is over
		s.finish()
	}
	return n, err
}

// Close ends the stream (for example when the agent disconnects).
func (s *usageStreamReader) Close() error {
	s.finish()
	if err := s.src.Close(); err != nil {
		return fmt.Errorf("close upstream stream: %w", err)
	}
	return nil
}

// scan splits bytes into lines. A line can be cut across two Reads, so the
// unfinished part waits in s.pending until its '\n' arrives.
func (s *usageStreamReader) scan(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if !s.skipping {
				s.pending = append(s.pending, b...)
				if len(s.pending) > maxSSELine {
					s.pending, s.skipping = s.pending[:0], true
				}
			}
			return
		}
		if !s.skipping {
			s.pending = append(s.pending, b[:i]...)
			s.handleLine(s.pending)
		}
		s.pending, s.skipping = s.pending[:0], false
		b = b[i+1:]
	}
}

// handleLine looks at one SSE line, e.g.
//
//	data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34}}
func (s *usageStreamReader) handleLine(line []byte) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return // comments, "event:" lines, blank separators
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || !bytes.Contains(data, []byte(`"usage"`)) {
		return // "[DONE]" or a chunk without usage: skip without decoding
	}
	u, err := ParseUsage(data)
	if err != nil {
		return // "usage": null on normal chunks
	}
	s.last, s.found = u, true
}

func (s *usageStreamReader) finish() {
	s.once.Do(func() {
		if !s.skipping && len(s.pending) > 0 {
			s.handleLine(s.pending) // last line had no trailing '\n'
		}
		s.onDone(s.last, s.found)
	})
}
