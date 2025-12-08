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



curl -X POST "http://localhost:30000/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"Qwen3-8B","messages":[{"role":"user","content":"你好，请用中文回复"}],"max_tokens":50}'



  curl -X POST "http://localhost:30000/generate" \
  -H "Content-Type: application/json" \
  -d '{"text":"Hello, how are you?","sampling_params":{"temperature":0.7,"max_new_tokens":50}}'



  curl -X POST "http://10.10.0.14:30797/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"Qwen3-8B","messages":[{"role":"user","content":"你好"}],"max_tokens":30}'



  curl -s "http://10.10.0.14:30797/analytics?service=sglang-proxy-service&start=2025-01-01T00:00:00Z&end=2025-01-02T00:00:00Z" | jq .



  # docker build --platform linux/amd64 -t model_proxy:latest .
# docker run -d -p 9000:9000 model_proxy:latest

# docker tag model_proxy:latest default-artifact.tencentcloudcr.com/dev/model_proxy:v1.0.13
# docker push default-artifact.tencentcloudcr.com/dev/model_proxy:v1.0.13



# apiVersion: v1
# kind: Secret
# metadata:
#   name: tencent-registry-secret
#   namespace: default
# type: kubernetes.io/dockerconfigjson
# stringData:
#   .dockerconfigjson: |
#     {
#       "auths": {
#         "default-artifact.tencentcloudcr.com": {
#           "username": "tcr$tenyunw",
#           "password": "RSw2KCGXdOh00S43KozQTwEr9Kp2BrJu",
#           "email": "your-email@example.com",
#           "auth": "dGNyJHRlbnl1bnc6UlN3MktDR1hkT2gwMFM0M0tvelFUd0VyOUtwMkJySnU="
#         }
#       }
#     }


# kubectl create secret docker-registry tencent-registry-secret \
#   --docker-server=default-artifact.tencentcloudcr.com \
#   --docker-username='tcr$tenyunw' \
#   --docker-password='RSw2KCGXdOh00S43KozQTwEr9Kp2BrJu' \
#   --docker-email='your-email@example.com' \
#   -n default

# curl -X POST "http://localhost:9000/v1/chat/completions" \
#   -H "Content-Type: application/json" \
#   -d '{
#     "model": "Qwen3-8B",
#     "messages": [{"role": "user", "content": "请解释量子力学"}],
#     "thinking": true,
#     "max_tokens": 2000
#   }'