# Sandboxed Code Execution Service

A Go service that takes untrusted, user-submitted code and runs it safely — the kind of engine that sits behind LeetCode, Codeforces, or Judge0. It accepts a code submission over HTTP, executes it inside a locked-down sandbox, and returns stdout/stderr/exit code, without letting the submission touch anything it shouldn't.

## The problem

Letting strangers run arbitrary code on your infrastructure is inherently adversarial. A submission — accidentally or on purpose — could:

- hang forever, or fork itself into a denial-of-service
- exhaust memory or spawn unbounded processes
- read files it has no business touching (other users' data, host secrets, the server's own source)
- reach out over the network to attack something else
- try to escape the sandbox itself (`ptrace`, `chroot` tricks, raw syscalls)

This service handles all of that by stripping a process down, layer by layer, to only the capabilities it legitimately needs to compute an answer.

## Isolation layers

Each layer closes a specific gap left by the one before it. Full build log with the reasoning, dead ends, and on-VM verification for each: **[THREAT_MODEL.md](THREAT_MODEL.md)**.

| Layer | Mechanism | Stops |
|---|---|---|
| Time limit | `context.WithTimeout` + process-group `SIGKILL` | Infinite loops, hung processes |
| Resource limits | cgroup v2 `pids.max` / `memory.max` | Fork bombs, memory exhaustion |
| Output cap | 1MB stdout/stderr limit | Disk/log-filling output floods |
| Filesystem isolation | `chroot` into a minimal jail + mount namespace | Reading host files, other submissions' data |
| Process isolation | PID namespace | Seeing or signaling other processes on the host |
| Concurrency | Bounded worker pool + queue | One slow submission blocking every other request |
| Network isolation | Empty network namespace (`CLONE_NEWNET`) | Outbound requests, DNS exfiltration, SSRF |
| Syscall filtering | seccomp-bpf whitelist, `SCMP_ACT_KILL` | `ptrace`, `mount`, raw sockets, anything not explicitly allowed — even attack vectors nobody thought to name individually |

Every layer above has a corresponding adversarial test in [`tests/evil/`](tests/evil/) that verifies it actually holds.

## API

**`POST /submit`**

```bash
curl -X POST localhost:8080/submit \
  -H 'Content-Type: application/json' \
  -d '{"code": "print(\"hello world\")", "language": "python"}'
```

```json
{
  "status": "success",
  "stdout": "hello world\n",
  "stderr": "",
  "exit_code": 0,
  "elapsed_ms": 44
}
```

`status` is one of:

| Status | Meaning |
|---|---|
| `success` | Ran to completion within all limits |
| `runtime_error` | Non-zero exit, or killed by the seccomp filter / another sandbox boundary |
| `time_limit_exceeded` | Hit the execution deadline |
| `memory_limit_exceeded` | Killed by the cgroup OOM killer |
| `process_limit_exceeded` | Hit the cgroup `pids.max` cap |
| `output_limit_exceeded` | Exceeded the stdout/stderr cap |
| `queue_timeout` | Sat in the queue too long without reaching a worker |
| `internal_error` | Sandbox setup itself failed (not the submission's fault) |

A `503` means the queue is full — the server is honestly saturated rather than silently piling up unbounded work.

## Requirements

This only runs on Linux — it depends on cgroup v2, Linux namespaces, and seccomp-bpf, none of which exist on macOS. Development happens on macOS with a Linux VM (Multipass) for building, running, and testing.

- Linux kernel with cgroup v2 (unified hierarchy) mounted at `/sys/fs/cgroup`
- Go 1.26+
- `libseccomp-dev` (the C library `libseccomp-golang` binds to)
- Python 3 on the host, to build the jail
- A minimal jail rootfs at `/opt/jail-template` — see below

## Setup

```bash
sudo apt install -y libseccomp-dev python3
go build -o server ./cmd/server
```

**Jail template**: `/opt/jail-template` needs to be a minimal root filesystem containing the `python3` binary, its shared libraries (including `libffi`, which `ctypes` needs but a plain `ldd python3` won't reveal), and the Python standard library. This directory is host-local setup, not part of the repo — each submission's jail is a fresh `cp -r` of it (`cmd/server/worker.go`).

Run it:

```bash
sudo ./server
# listening on :8080
```

(`sudo` because setting up cgroups, namespaces, and `chroot` all require root.)

## Testing

```bash
./evil_load_test.sh
```

Fires every file in `tests/evil/` — fork bombs, memory bombs, infinite loops, host file reads, network exfiltration attempts, a `chroot` escape, a `ptrace`-based sandbox escape — concurrently alongside normal submissions, and prints each result. Every evil payload should land on a contained/blocked outcome; normal submissions should all succeed.

## Project structure

```
cmd/server/       HTTP API, worker pool, sandbox execution, seccomp filter
cmd/poc/          throwaway os/exec proof-of-concept, not part of the service
tests/evil/       adversarial test cases, one per attack vector
evil_load_test.sh runs the full evil suite concurrently against a running server
THREAT_MODEL.md   phase-by-phase build log and design rationale
```

## Status

A from-scratch build exercise in sandboxing and backend engineering, not a production-hardened service — see [THREAT_MODEL.md](THREAT_MODEL.md) for exactly what's been verified and what's still open at each layer.
