package main

import (
    "encoding/json"
    "fmt"
    "log"
    "math/rand"
    "net/http"
    "strings"
    "time"
)

type GenerateRequest struct {
    Text           string `json:"text"`
    SamplingParams struct {
        Temperature   float64 `json:"temperature"`
        MaxNewTokens  int     `json:"max_new_tokens"`
    } `json:"sampling_params"`
}

type GenerateResponse struct {
    ID            string `json:"id"`
    GeneratedText string `json:"generated_text"`
    DurationMs    int64  `json:"duration_ms"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]interface{}{
        "status":    "healthy",
        "model":     "mock-llm-v1",
        "timestamp": time.Now().Format(time.RFC3339),
    })
}

// 简单同步生成接口，返回完整 JSON
func generateHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    var req GenerateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }

    start := time.Now()
    // 模拟推理延迟（依据输入长度和采样参数）
    latency := 50 + rand.Intn(200) + len(req.Text)*2
    time.Sleep(time.Duration(latency) * time.Millisecond)

    // 简单生成：重复输入并追加伪内容
    out := strings.TrimSpace(req.Text)
    if out == "" {
        out = "Hello from mock-llm."
    }
    // 根据 max_new_tokens 生成重复片段
    tokens := req.SamplingParams.MaxNewTokens
    if tokens <= 0 {
        tokens = 20
    }
    add := strings.Repeat(" " + out, tokens/10+1)
    generated := fmt.Sprintf("%s -- MOCK-GENERATION%s", out, add)

    resp := GenerateResponse{
        ID:            fmt.Sprintf("mock-%d", time.Now().UnixNano()),
        GeneratedText: generated,
        DurationMs:    time.Since(start).Milliseconds(),
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(resp)
}

// 流式接口示例（chunked response），客户端可以逐步读取
func generateStreamHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "streaming unsupported", http.StatusInternalServerError)
        return
    }

    var req GenerateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.WriteHeader(http.StatusOK)

    // 模拟分多次输出 token（每次一段）
    base := strings.TrimSpace(req.Text)
    if base == "" {
        base = "Hello from mock-llm."
    }
    max := req.SamplingParams.MaxNewTokens
    if max <= 0 {
        max = 20
    }
    for i := 0; i < max; i += 5 {
        chunk := fmt.Sprintf("data: token_chunk_%d: %s\n\n", i, base)
        _, _ = w.Write([]byte(chunk))
        flusher.Flush()
        time.Sleep(150 * time.Millisecond) // 模拟流式生成延迟
    }
    // 结束标记
    _, _ = w.Write([]byte("data: [DONE]\n\n"))
    flusher.Flush()
}

func main() {
    rand.Seed(time.Now().UnixNano())

    http.HandleFunc("/health", healthHandler)
    http.HandleFunc("/v1/generate", generateHandler)
    http.HandleFunc("/v1/generate_stream", generateStreamHandler)

    addr := ":30000"
    log.Printf("mock-llm server listening on %s", addr)
    if err := http.ListenAndServe(addr, nil); err != nil {
        log.Fatalf("server failed: %v", err)
    }
}