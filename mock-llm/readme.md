```bash
curl -X POST "http://localhost:30000/v1/generate" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, how are you?",
    "sampling_params": {"temperature": 0.7, "max_new_tokens": 50}
  }' | jq .

```



```bash
  curl -N -X POST "http://localhost:30000/v1/generate_stream" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, stream!",
    "sampling_params": {"temperature": 0.7, "max_new_tokens": 30}
  }'
  
```


# health
curl -sS http://localhost:30000/health | jq .

# 同步生成接口
curl -sS -X POST "http://localhost:30000/v1/generate" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, how are you?",
    "sampling_params": {"temperature": 0.7, "max_new_tokens": 50}
  }' | jq .

# 流式接口（逐块输出）
curl -N -X POST "http://localhost:30000/v1/generate_stream" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, stream!",
    "sampling_params": {"temperature": 0.7, "max_new_tokens": 30}
  }'