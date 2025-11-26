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