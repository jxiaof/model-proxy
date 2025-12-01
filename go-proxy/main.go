package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type ProxyServer struct {
	db          *sql.DB
	podName     string
	namespace   string
	serviceName string
	sglangURL   string
	httpClient  *http.Client
}

type RequestLog struct {
	PodName     string    `json:"pod_name"`
	Namespace   string    `json:"namespace"`
	ServiceName string    `json:"service_name"`
	RequestTime time.Time `json:"request_time"`
	Path        string    `json:"path"`
	Method      string    `json:"method"`
	StatusCode  int       `json:"status_code"`
	IsThinkMode bool      `json:"is_think_mode"`
	Duration    int64     `json:"duration_ms"`
}

func NewProxyServer() (*ProxyServer, error) {
	// 获取 Pod 信息
	podName := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")
	serviceName := os.Getenv("SERVICE_NAME")
	sglangURL := os.Getenv("SGLANG_URL")

	if podName == "" {
		podName = "unknown-fake-pod"
	}
	if namespace == "" {
		namespace = "default"
	}
	if serviceName == "" {
		serviceName = "sglang-service"
	}

	if podName == "" || namespace == "" || serviceName == "" {
		return nil, fmt.Errorf("missing required environment variables")
	}

	if sglangURL == "" {
		sglangURL = "http://localhost:30000" // 默认 SGlang 端口
	}

	// 连接 MySQL
	mysqlDSN := os.Getenv("MYSQL_DSN")
	if mysqlDSN == "" {
		mysqlDSN = "root:Admin@798!@tcp(gz-cdb-ihelunwl.sql.tencentcdb.com:28531)/tycloud?charset=utf8mb4&parseTime=True&loc=Local"
	}

	var db *sql.DB
	var err error

	// 重试连接 MySQL
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", mysqlDSN)
		if err == nil {
			err = db.Ping()
			if err == nil {
				break
			}
		}
		log.Printf("Waiting for MySQL connection... (attempt %d/30)", i+1)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %v", err)
	}

	// 初始化数据库表
	if err := initDatabase(db); err != nil {
		return nil, fmt.Errorf("failed to init database: %v", err)
	}

	// 配置 HTTP 客户端支持长连接和 Think 模式的超长等待
	httpClient := &http.Client{
		Timeout: 600 * time.Second, // 10分钟超时，适合 Think 模式
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       120 * time.Second,
			DisableKeepAlives:     false,
			ResponseHeaderTimeout: 300 * time.Second, // Think 模式可能需要更长的首字节等待
			ExpectContinueTimeout: 10 * time.Second,
		},
	}

	return &ProxyServer{
		db:          db,
		podName:     podName,
		namespace:   namespace,
		serviceName: serviceName,
		sglangURL:   sglangURL,
		httpClient:  httpClient,
	}, nil
}

func initDatabase(db *sql.DB) error {
	createTableSQL := `
    CREATE TABLE IF NOT EXISTS request_logs (
        id BIGINT AUTO_INCREMENT PRIMARY KEY,
        pod_name VARCHAR(255) NOT NULL,
        namespace VARCHAR(255) NOT NULL,
        service_name VARCHAR(255) NOT NULL,
        request_time DATETIME NOT NULL,
        path VARCHAR(500),
        method VARCHAR(10),
        status_code INT,
        is_think_mode TINYINT(1) DEFAULT 0,
        duration_ms BIGINT DEFAULT 0,
        INDEX idx_pod_time (pod_name, request_time),
        INDEX idx_service_time (service_name, request_time),
        INDEX idx_think_mode (is_think_mode, request_time)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
    `

	_, err := db.Exec(createTableSQL)
	return err
}

func (ps *ProxyServer) logRequest(ctx context.Context, path, method string, statusCode int, isThinkMode bool, durationMs int64) error {
	query := `
        INSERT INTO request_logs (pod_name, namespace, service_name, request_time, path, method, status_code, is_think_mode, duration_ms)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `

	_, err := ps.db.ExecContext(ctx, query,
		ps.podName,
		ps.namespace,
		ps.serviceName,
		time.Now(),
		path,
		method,
		statusCode,
		isThinkMode,
		durationMs,
	)

	return err
}

// 检测是否为 Think 模式请求
func isThinkModeRequest(body []byte) bool {
	// 检测请求体中是否包含 Think 模式相关参数
	var reqBody map[string]interface{}
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return false
	}

	// 检查常见的 Think 模式标志
	// 1. thinking 参数
	if thinking, ok := reqBody["thinking"].(bool); ok && thinking {
		return true
	}

	// 2. enable_thinking 参数
	if enableThinking, ok := reqBody["enable_thinking"].(bool); ok && enableThinking {
		return true
	}

	// 3. 检查 messages 中是否有 think 指令
	if messages, ok := reqBody["messages"].([]interface{}); ok {
		for _, msg := range messages {
			if m, ok := msg.(map[string]interface{}); ok {
				if content, ok := m["content"].(string); ok {
					if strings.Contains(strings.ToLower(content), "/think") ||
						strings.Contains(strings.ToLower(content), "think step by step") ||
						strings.Contains(strings.ToLower(content), "let's think") {
						return true
					}
				}
			}
		}
	}

	// 4. 检查 sampling_params 中的 thinking 参数
	if samplingParams, ok := reqBody["sampling_params"].(map[string]interface{}); ok {
		if thinking, ok := samplingParams["thinking"].(bool); ok && thinking {
			return true
		}
	}

	return false
}

// 检测响应是否包含 Think 内容
func containsThinkContent(data []byte) bool {
	return bytes.Contains(data, []byte("<think>")) ||
		bytes.Contains(data, []byte("</think>")) ||
		bytes.Contains(data, []byte(`"thinking"`)) ||
		bytes.Contains(data, []byte(`"reasoning_content"`))
}

// 通用 CORS 处理器
func enableCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
	w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Tenant-ID, Accept-Language, X-Request-ID, X-Internal-Timestamp, X-Internal-Signature, X-Internal-Nonce, X-Internal-Call")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Max-Age", "86400")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// 代理处理器 - 支持流式响应、CORS 和 Think 模式
func (ps *ProxyServer) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 处理 CORS
	if enableCORS(w, r) {
		return
	}

	startTime := time.Now()

	// 构建目标 URL
	targetURL := ps.sglangURL + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// 读取请求体
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}

	// 检测是否为 Think 模式
	isThinkMode := isThinkModeRequest(bodyBytes)
	if isThinkMode {
		log.Printf("Think mode detected for request: %s %s", r.Method, r.URL.Path)
	}

	// 创建新请求 - Think 模式使用更长的超时上下文
	var ctx context.Context
	var cancel context.CancelFunc
	if isThinkMode {
		ctx, cancel = context.WithTimeout(r.Context(), 600*time.Second) // 10分钟
	} else {
		ctx, cancel = context.WithTimeout(r.Context(), 300*time.Second) // 5分钟
	}
	defer cancel()

	proxyReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		log.Printf("Error creating proxy request: %v", err)
		http.Error(w, "Proxy error", http.StatusInternalServerError)
		go ps.logRequest(context.Background(), r.URL.Path, r.Method, http.StatusInternalServerError, isThinkMode, time.Since(startTime).Milliseconds())
		return
	}

	// 复制请求头
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// 发送请求
	resp, err := ps.httpClient.Do(proxyReq)
	if err != nil {
		log.Printf("Error proxying request to SGlang: %v", err)
		http.Error(w, "SGlang service unavailable", http.StatusBadGateway)
		go ps.logRequest(context.Background(), r.URL.Path, r.Method, http.StatusBadGateway, isThinkMode, time.Since(startTime).Milliseconds())
		return
	}
	defer resp.Body.Close()

	// 检测响应是否为流式（SSE）
	isStreamResponse := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") ||
		strings.Contains(resp.Header.Get("Content-Type"), "application/x-ndjson") ||
		resp.Header.Get("Transfer-Encoding") == "chunked"

	// 复制响应头（排除可能冲突的 CORS 头）
	for key, values := range resp.Header {
		if key == "Access-Control-Allow-Origin" ||
			key == "Access-Control-Allow-Methods" ||
			key == "Access-Control-Allow-Headers" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 对于流式响应，确保禁用缓冲
	if isStreamResponse {
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 流式复制响应体 - 支持 SSE 和 Think 模式
	var detectedThinkInResponse bool
	if flusher, ok := w.(http.Flusher); ok {
		buffer := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buffer)
			if n > 0 {
				// 检测响应中是否包含 Think 内容
				if !detectedThinkInResponse && containsThinkContent(buffer[:n]) {
					detectedThinkInResponse = true
					isThinkMode = true // 更新为 Think 模式
				}

				if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
					log.Printf("Error writing response: %v", writeErr)
					break
				}
				flusher.Flush() // 立即刷新数据到客户端
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				log.Printf("Error reading response body: %v", readErr)
				break
			}
		}
	} else {
		// 普通传输
		io.Copy(w, resp.Body)
	}

	duration := time.Since(startTime)

	// 记录请求日志
	go ps.logRequest(context.Background(), r.URL.Path, r.Method, resp.StatusCode, isThinkMode, duration.Milliseconds())

	modeStr := ""
	if isThinkMode {
		modeStr = " [THINK]"
	}
	log.Printf("Request proxied%s: %s %s -> Pod: %s, Status: %d, Duration: %v",
		modeStr, r.Method, r.URL.Path, ps.podName, resp.StatusCode, duration)
}

// 健康检查
func (ps *ProxyServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	if enableCORS(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"pod":       ps.podName,
		"namespace": ps.namespace,
		"service":   ps.serviceName,
		"features":  []string{"streaming", "think-mode", "cors"},
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// 统一响应结构
type Response struct {
	Code    int         `json:"code"`
	Msg     string      `json:"msg"`
	Data    interface{} `json:"data"`
	Details string      `json:"details,omitempty"`
}

// 成功响应
func successResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Code: 0,
		Msg:  "success",
		Data: data,
	})
}

// 错误响应
func errorResponse(w http.ResponseWriter, code int, msg string, detail string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(Response{
		Code:    code,
		Msg:     msg,
		Data:    nil,
		Details: detail,
	})
}

// 统计信息
func (ps *ProxyServer) statsHandler(w http.ResponseWriter, r *http.Request) {
	if enableCORS(w, r) {
		return
	}

	query := `
        SELECT 
            pod_name,
            COUNT(*) as request_count,
            SUM(CASE WHEN is_think_mode = 1 THEN 1 ELSE 0 END) as think_mode_count,
            AVG(duration_ms) as avg_duration_ms,
            MIN(request_time) as first_request,
            MAX(request_time) as last_request
        FROM request_logs
        WHERE service_name = ? 
        GROUP BY pod_name
        ORDER BY request_count DESC
    `

	rows, err := ps.db.Query(query, ps.serviceName)
	if err != nil {
		errorResponse(w, 500, "database error", err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type PodStats struct {
		PodName        string    `json:"pod_name"`
		RequestCount   int       `json:"request_count"`
		ThinkModeCount int       `json:"think_mode_count"`
		AvgDurationMs  float64   `json:"avg_duration_ms"`
		FirstRequest   time.Time `json:"first_request"`
		LastRequest    time.Time `json:"last_request"`
	}

	var stats []PodStats
	for rows.Next() {
		var s PodStats
		if err := rows.Scan(&s.PodName, &s.RequestCount, &s.ThinkModeCount, &s.AvgDurationMs, &s.FirstRequest, &s.LastRequest); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		stats = append(stats, s)
	}

	if err := rows.Err(); err != nil {
		errorResponse(w, 500, "database scan error", err.Error(), http.StatusInternalServerError)
		return
	}

	successResponse(w, map[string]interface{}{
		"service":   ps.serviceName,
		"namespace": ps.namespace,
		"stats":     stats,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Pod 负载信息
func (ps *ProxyServer) podLoadHandler(w http.ResponseWriter, r *http.Request) {
	if enableCORS(w, r) {
		return
	}

	q := r.URL.Query()
	service := q.Get("service")
	if service == "" {
		service = ps.serviceName
	}

	var (
		now      = time.Now()
		startStr = q.Get("start")
		endStr   = q.Get("end")
		start    time.Time
		end      time.Time
		err      error
	)

	if startStr == "" {
		start = now.Add(-1 * time.Hour)
	} else {
		start, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			errorResponse(w, 400, "invalid parameter", "invalid start (use RFC3339 format, e.g. 2025-01-01T00:00:00Z)", http.StatusBadRequest)
			return
		}
	}
	if endStr == "" {
		end = now
	} else {
		end, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			errorResponse(w, 400, "invalid parameter", "invalid end (use RFC3339 format, e.g. 2025-01-01T23:59:59Z)", http.StatusBadRequest)
			return
		}
	}
	if !end.After(start) {
		errorResponse(w, 400, "invalid parameter", "end must be after start", http.StatusBadRequest)
		return
	}

	query := `
        SELECT 
            pod_name,
            COUNT(*) as request_count,
            SUM(CASE WHEN is_think_mode = 1 THEN 1 ELSE 0 END) as think_mode_count,
            AVG(status_code) as avg_status_code,
            AVG(duration_ms) as avg_duration_ms,
            MIN(request_time) as first_request,
            MAX(request_time) as last_request
        FROM request_logs
        WHERE service_name = ? AND request_time BETWEEN ? AND ?
        GROUP BY pod_name
        ORDER BY request_count DESC
    `

	rows, err := ps.db.Query(query, service, start, end)
	if err != nil {
		errorResponse(w, 500, "database error", err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type PodLoad struct {
		PodName        string    `json:"pod_name"`
		RequestCount   int       `json:"request_count"`
		ThinkModeCount int       `json:"think_mode_count"`
		AvgStatusCode  float64   `json:"avg_status_code"`
		AvgDurationMs  float64   `json:"avg_duration_ms"`
		FirstRequestAt time.Time `json:"first_request"`
		LastRequestAt  time.Time `json:"last_request"`
	}
	var loads []PodLoad
	for rows.Next() {
		var pl PodLoad
		if err := rows.Scan(&pl.PodName, &pl.RequestCount, &pl.ThinkModeCount, &pl.AvgStatusCode, &pl.AvgDurationMs, &pl.FirstRequestAt, &pl.LastRequestAt); err != nil {
			log.Printf("Error scanning pod load row: %v", err)
			continue
		}
		loads = append(loads, pl)
	}

	if err := rows.Err(); err != nil {
		errorResponse(w, 500, "database scan error", err.Error(), http.StatusInternalServerError)
		return
	}

	successResponse(w, map[string]interface{}{
		"service":   service,
		"namespace": ps.namespace,
		"start":     start.Format(time.RFC3339),
		"end":       end.Format(time.RFC3339),
		"pods":      loads,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Think 模式统计 API
func (ps *ProxyServer) thinkStatsHandler(w http.ResponseWriter, r *http.Request) {
	if enableCORS(w, r) {
		return
	}

	q := r.URL.Query()
	service := q.Get("service")
	if service == "" {
		service = ps.serviceName
	}

	query := `
        SELECT 
            is_think_mode,
            COUNT(*) as request_count,
            AVG(duration_ms) as avg_duration_ms,
            MAX(duration_ms) as max_duration_ms,
            MIN(duration_ms) as min_duration_ms
        FROM request_logs
        WHERE service_name = ? AND request_time >= DATE_SUB(NOW(), INTERVAL 24 HOUR)
        GROUP BY is_think_mode
    `

	rows, err := ps.db.Query(query, service)
	if err != nil {
		errorResponse(w, 500, "database error", err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type ModeStats struct {
		IsThinkMode   bool    `json:"is_think_mode"`
		RequestCount  int     `json:"request_count"`
		AvgDurationMs float64 `json:"avg_duration_ms"`
		MaxDurationMs int64   `json:"max_duration_ms"`
		MinDurationMs int64   `json:"min_duration_ms"`
	}

	var modeStats []ModeStats
	for rows.Next() {
		var ms ModeStats
		if err := rows.Scan(&ms.IsThinkMode, &ms.RequestCount, &ms.AvgDurationMs, &ms.MaxDurationMs, &ms.MinDurationMs); err != nil {
			log.Printf("Error scanning think stats row: %v", err)
			continue
		}
		modeStats = append(modeStats, ms)
	}

	successResponse(w, map[string]interface{}{
		"service":    service,
		"namespace":  ps.namespace,
		"mode_stats": modeStats,
		"timestamp":  time.Now().Format(time.RFC3339),
	})
}

func (ps *ProxyServer) Close() error {
	if ps.db != nil {
		return ps.db.Close()
	}
	return nil
}

func main() {
	proxy, err := NewProxyServer()
	if err != nil {
		log.Fatalf("Failed to create proxy server: %v", err)
	}
	defer proxy.Close()

	// 使用 ServeMux 确保路由精确匹配
	mux := http.NewServeMux()

	// 精确路由优先注册
	mux.HandleFunc("/health", proxy.healthHandler)
	mux.HandleFunc("/stats", proxy.statsHandler)
	mux.HandleFunc("/pod-load", proxy.podLoadHandler)
	mux.HandleFunc("/think-stats", proxy.thinkStatsHandler)

	// 通配路由最后注册
	mux.HandleFunc("/", proxy.proxyHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("Starting proxy server on port %s (Pod: %s) with CORS and Think mode enabled", port, proxy.podName)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
