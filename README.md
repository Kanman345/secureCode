# Sandboxed Code Execution Service

We're building a sandboxed code execution service — the kind of system that powers platforms like LeetCode, Codeforces, and Judge0 under the hood. The core problem it solves is trust: when a platform lets strangers submit arbitrary code and runs it on shared server infrastructure, that code could accidentally or maliciously hang the server with an infinite loop, exhaust memory or spawn endless processes, read files it shouldn't (other users' data, secrets, credentials), or reach out over the network to attack other systems. The project's job is to take untrusted user-submitted code and execute it safely by stripping away, layer by layer, the default capabilities any process would normally have — enforcing time limits, memory/process limits, filesystem isolation, and (as a stretch) network isolation and syscall restrictions — so the code can compute its answer and nothing else. Alongside the sandboxing itself, it's also a real backend engineering exercise: an API that accepts submissions, a worker pool that executes them concurrently and safely, and a result-reporting layer, all built to survive genuinely adversarial input.

## Threat Model Log

Phase 1 writes each submission to its own temp directory, but this is filesystem convenience, not isolation — the executed process can still read/write anywhere the server process can, spawn other processes, and reach the network.

Phase 2 adds a hard time limit via `context.WithTimeout` + `exec.CommandContext`, so a hung submission is killed and the server stays responsive. However, this only signals the direct child process — a submission that forks children (e.g. a fork bomb) can leave orphaned processes running after the parent is killed. Process-group-based cleanup is deferred to Phase 3.

