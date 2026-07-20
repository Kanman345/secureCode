#!/usr/bin/env bash
cd "$(dirname "$0")"

# Each job's output goes to its own file instead of the shared terminal --
# concurrent processes writing JSON blobs bigger than one pipe buffer
# (PIPE_BUF, 4KB on Linux) directly to the same stdout interleave mid-write,
# producing spliced/corrupted output that looks like nondeterministic results
# but is actually just an unsynchronized-writer artifact.
outdir=$(mktemp -d)
trap 'rm -rf "$outdir"' EXIT

to_json_string() {
  python3 -c "import json,sys; print(json.dumps(open(sys.argv[1]).read()))" "$1"
}

submit_evil() {
  local file="$1"
  local name
  name="$(basename "$file")"
  local code_json
  code_json=$(to_json_string "$file")
  {
    echo "=== $name ==="
    curl -s -X POST http://localhost:8080/submit \
      -d "{\"code\": $code_json, \"language\": \"python\"}" | python3 -m json.tool
  } > "$outdir/evil-$name.out" 2>&1
}

submit_normal() {
  local n="$1"
  {
    echo "=== normal-$n ==="
    curl -s -X POST http://localhost:8080/submit \
      -d '{"code":"print(2+2)","language":"python"}' | python3 -m json.tool
  } > "$outdir/normal-$n.out" 2>&1
}

for f in tests/evil/*.py; do
  submit_evil "$f" &
done

for i in 1 2 3 4; do
  submit_normal "$i" &
done

wait

for f in "$outdir"/*.out; do
  cat "$f"
  echo
done
