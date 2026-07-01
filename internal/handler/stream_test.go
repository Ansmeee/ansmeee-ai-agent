package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestEnsureJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON string
	}{
		{"valid object", `{"key":"value"}`, `{"key":"value"}`},
		{"valid array", `[1,2,3]`, `[1,2,3]`},
		{"valid string", `"hello"`, `"hello"`},
		{"valid number", `42`, `42`},
		{"plain text", `hello world`, `"hello world"`},
		{"empty", ``, `""`},
		{"broken json", `{"key":}`, `"{\"key\":}"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureJSON(tt.input)
			if string(got) != tt.wantJSON {
				t.Errorf("ensureJSON(%q) = %s, want %s", tt.input, got, tt.wantJSON)
			}
			if !json.Valid(got) {
				t.Errorf("ensureJSON(%q) produced invalid JSON: %s", tt.input, got)
			}
		})
	}
}

type testFlusher struct {
	*httptest.ResponseRecorder
}

func (f *testFlusher) Flush() {
	f.ResponseRecorder.Flush()
}

func newTestFlusher() (*testFlusher, http.ResponseWriter) {
	rec := httptest.NewRecorder()
	tf := &testFlusher{rec}
	return tf, tf
}

func TestWriteSSEJSON(t *testing.T) {
	tf, w := newTestFlusher()

	writeSSEJSON(w, tf, "chunk", sseChunkData{Content: "hello"})

	body := tf.Body.String()
	if !strings.Contains(body, "event: chunk\n") {
		t.Errorf("missing event line, got: %q", body)
	}
	if !strings.Contains(body, `data: {"content":"hello"}`) {
		t.Errorf("missing JSON data, got: %q", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Error("SSE event should end with double newline")
	}
}

func TestWriteSSEJSON_AllTypes(t *testing.T) {
	tests := []struct {
		event string
		data  any
		check string
	}{
		{"session", sseSessionData{SessionID: "s1"}, `"session_id":"s1"`},
		{"thinking", sseThinkingData{Iteration: 2}, `"iteration":2`},
		{"tool_call", sseToolCallData{ToolCallID: "c1", Name: "calc", Arguments: json.RawMessage(`{"x":1}`), Result: json.RawMessage(`"4"`), Success: true}, `"success":true`},
		{"error", sseErrorData{Message: "fail"}, `"message":"fail"`},
	}

	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			tf, w := newTestFlusher()
			writeSSEJSON(w, tf, tt.event, tt.data)
			body := tf.Body.String()

			if !strings.Contains(body, "event: "+tt.event+"\n") {
				t.Errorf("missing event line for %s", tt.event)
			}

			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "data: ") {
					jsonStr := strings.TrimPrefix(line, "data: ")
					if !json.Valid([]byte(jsonStr)) {
						t.Errorf("data is not valid JSON: %s", jsonStr)
					}
					if !strings.Contains(jsonStr, tt.check) {
						t.Errorf("data %q doesn't contain %q", jsonStr, tt.check)
					}
				}
			}
		})
	}
}

func TestWriteSSEJSON_SpecialChars(t *testing.T) {
	tf, w := newTestFlusher()
	writeSSEJSON(w, tf, "chunk", sseChunkData{Content: `he said "hello" & <world>`})
	body := tf.Body.String()

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			jsonStr := strings.TrimPrefix(line, "data: ")
			var parsed sseChunkData
			if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}
			if parsed.Content != `he said "hello" & <world>` {
				t.Errorf("content mismatch: %q", parsed.Content)
			}
		}
	}
}

func TestHandle_EmptyMessage(t *testing.T) {
	h := &StreamHandler{}
	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"message":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completion", body)
	req.Header.Set("Content-Type", "application/json")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/chat/completion", h.Handle)
	r.ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if code, ok := resp["code"].(float64); !ok || code == 0 {
		t.Error("expected error response for empty message")
	}
}
