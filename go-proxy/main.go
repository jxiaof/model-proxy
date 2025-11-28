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

	// 配置 HTTP 客户端支持长连接
	httpClient := &http.Client{
		Timeout: 300 * time.Second, // 5分钟超时，适合长连接
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false, // 启用 Keep-Alive
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
		INDEX idx_pod_time (pod_name, request_time),
		INDEX idx_service_time (service_name, request_time)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`

	_, err := db.Exec(createTableSQL)
	return err
}

func (ps *ProxyServer) logRequest(ctx context.Context, path, method string, statusCode int) error {
	query := `
		INSERT INTO request_logs (pod_name, namespace, service_name, request_time, path, method, status_code)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	_, err := ps.db.ExecContext(ctx, query,
		ps.podName,
		ps.namespace,
		ps.serviceName,
		time.Now(),
		path,
		method,
		statusCode,
	)

	return err
}

// 通用 CORS 处理器（用于全局处理）
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

// 代理处理器 - 支持流式响应和 CORS
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

	// 创建新请求
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		log.Printf("Error creating proxy request: %v", err)
		http.Error(w, "Proxy error", http.StatusInternalServerError)
		ps.logRequest(context.Background(), r.URL.Path, r.Method, http.StatusInternalServerError)
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
		ps.logRequest(context.Background(), r.URL.Path, r.Method, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 复制响应头（排除可能冲突的 CORS 头）
	for key, values := range resp.Header {
		if key == "Access-Control-Allow-Origin" ||
			key == "Access-Control-Allow-Methods" ||
			key == "Access-Control-Allow-Headers" {
			continue // 跳过后端的 CORS 头，使用代理设置的
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 流式复制响应体 - 支持 SSE 等流式协议
	if flusher, ok := w.(http.Flusher); ok {
		buffer := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buffer)
			if n > 0 {
				if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
					log.Printf("Error writing response: %v", writeErr)
					break
				}
				flusher.Flush() // 立即刷新数据到客户端
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Printf("Error reading response body: %v", err)
				break
			}
		}
	} else {
		// 普通传输
		io.Copy(w, resp.Body)
	}

	// 记录请求日志
	go ps.logRequest(context.Background(), r.URL.Path, r.Method, resp.StatusCode)

	log.Printf("Request proxied: %s %s -> Pod: %s, Status: %d, Duration: %v",
		r.Method, r.URL.Path, ps.podName, resp.StatusCode, time.Since(startTime))
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
		PodName      string    `json:"pod_name"`
		RequestCount int       `json:"request_count"`
		FirstRequest time.Time `json:"first_request"`
		LastRequest  time.Time `json:"last_request"`
	}

	var stats []PodStats
	for rows.Next() {
		var s PodStats
		if err := rows.Scan(&s.PodName, &s.RequestCount, &s.FirstRequest, &s.LastRequest); err != nil {
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
			AVG(status_code) as avg_status_code,
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
		AvgStatusCode  float64   `json:"avg_status_code"`
		FirstRequestAt time.Time `json:"first_request"`
		LastRequestAt  time.Time `json:"last_request"`
	}
	var loads []PodLoad
	for rows.Next() {
		var pl PodLoad
		if err := rows.Scan(&pl.PodName, &pl.RequestCount, &pl.AvgStatusCode, &pl.FirstRequestAt, &pl.LastRequestAt); err != nil {
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

	// 通配路由最后注册
	mux.HandleFunc("/", proxy.proxyHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("Starting proxy server on port %s (Pod: %s) with CORS enabled", port, proxy.podName)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
