-- 查看每个 Pod 的请求统计
SELECT 
    pod_name,
    COUNT(*) as total_requests,
    AVG(status_code) as avg_status,
    MIN(request_time) as first_request,
    MAX(request_time) as last_request
FROM request_logs
WHERE request_time >= DATE_SUB(NOW(), INTERVAL 1 HOUR)
GROUP BY pod_name
ORDER BY total_requests DESC;

-- 查看负载分布百分比
SELECT 
    pod_name,
    COUNT(*) as request_count,
    ROUND(COUNT(*) * 100.0 / (SELECT COUNT(*) FROM request_logs), 2) as percentage
FROM request_logs
GROUP BY pod_name
ORDER BY request_count DESC;

-- 查看时间序列分布
SELECT 
    pod_name,
    DATE_FORMAT(request_time, '%Y-%m-%d %H:%i:00') as time_bucket,
    COUNT(*) as requests
FROM request_logs
WHERE request_time >= DATE_SUB(NOW(), INTERVAL 1 HOUR)
GROUP BY pod_name, time_bucket
ORDER BY time_bucket, pod_name;