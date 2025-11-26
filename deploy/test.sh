#!/bin/bash

SERVICE_URL="http://<your-service-ip>"  # 替换为实际 Service IP

echo "Testing load balancer with 100 requests..."

for i in {1..100}; do
  curl -X POST "$SERVICE_URL/v1/generate" \
    -H "Content-Type: application/json" \
    -d '{
      "text": "Hello, how are you?",
      "sampling_params": {
        "temperature": 0.7,
        "max_new_tokens": 50
      }
    }' &
  
  if [ $((i % 10)) -eq 0 ]; then
    echo "Sent $i requests..."
    sleep 1
  fi
done

wait

echo "All requests sent.  Checking stats..."
sleep 2

curl "$SERVICE_URL/stats" | jq . 