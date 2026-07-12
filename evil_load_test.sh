#!/usr/bin/env bash
cd "$(dirname "$0")"

to_json_string() {
  python3 -c "import json,sys; print(json.dumps(open(sys.argv[1]).read()))" "$1"
}

submit_evil() {
  local file="$1"
  local code_json
  code_json=$(to_json_string "$file")
  echo "=== $(basename "$file") ==="
  curl -s -X POST http://localhost:8080/submit \
    -d "{\"code\": $code_json, \"language\": \"python\"}" | python3 -m json.tool
  echo
}

submit_normal() {
  local n="$1"
  echo "=== normal-$n ==="
  curl -s -X POST http://localhost:8080/submit \
    -d '{"code":"print(2+2)","language":"python"}' | python3 -m json.tool
  echo
}

for f in tests/evil/*.py; do
  submit_evil "$f" &
done

for i in 1 2 3 4; do
  submit_normal "$i" &
done

wait
