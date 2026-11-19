// Command fakellm is a tiny fake OpenAI-compatible LLM server for demos and
// manual testing. It needs no API key, no GPU and no internet.
//
// It answers POST /v1/chat/completions with a fixed reply and a "usage" object
// where tokens are estimated as (characters / 4), which is roughly how real
// English text tokenizes.
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Content string `json:"content"`
	} `json:"messages"`
}

func main() {
	addr := ":9090"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		addr = v
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", handleChat)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("fake LLM listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	promptChars := 0
	for _, m := range req.Messages {
		promptChars += len(m.Content)
	}
	answer := "Hello from fakellm! This is a pretend answer."
	in, out := max(1, promptChars/4), max(1, len(answer)/4)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-fake",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": answer},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     in,
			"completion_tokens": out,
			"total_tokens":      in + out,
		},
	})
}
