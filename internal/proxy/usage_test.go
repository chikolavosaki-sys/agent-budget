package proxy

import (
	"errors"
	"testing"
)

func TestParseUsage(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    Usage
		wantErr bool
		errIs   error // if set, the error must match this with errors.Is
	}{
		{
			name: "normal response",
			body: `{"model":"llama3.2","choices":[{"message":{"content":"hi"}}],
			        "usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`,
			want: Usage{Model: "llama3.2", InputTokens: 12, OutputTokens: 34},
		},
		{
			name: "zero tokens",
			body: `{"model":"m","usage":{"prompt_tokens":0,"completion_tokens":0}}`,
			want: Usage{Model: "m"},
		},
		{
			name:    "usage missing",
			body:    `{"model":"m","choices":[]}`,
			wantErr: true,
			errIs:   ErrNoUsage,
		},
		{
			name:    "broken json",
			body:    `{"usage":`,
			wantErr: true,
		},
		{
			name:    "empty body",
			body:    ``,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUsage([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errIs != nil && !errors.Is(err, tt.errIs) {
				t.Fatalf("error = %v, want %v", err, tt.errIs)
			}
			if got != tt.want {
				t.Errorf("ParseUsage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
