export PATH=$PATH:/usr/local/go/bin

export GOPROXY=https://goproxy.cn,direct




curl -s http://localhost:9000/health | jq .



curl -X POST "http://localhost:9000/v1/generate" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, how are you?",
    "sampling_params": {
      "temperature": 0.7,
      "max_new_tokens": 50
    }
  }' | jq .

curl -N -X POST "http://localhost:9000/v1/generate_stream" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, stream!",
    "sampling_params": {
      "temperature": 0.7,
      "max_new_tokens": 30
    }
  }'


SERVICE_URL="http://localhost:9000"

for i in {1..10}; do
  curl -X POST "$SERVICE_URL/v1/generate" \
    -H "Content-Type: application/json" \
    -d '{"text": "Test request '$i'", "sampling_params": {"temperature": 0.7, "max_new_tokens": 50}}' &
done

wait
curl -s "$SERVICE_URL/stats" | jq .