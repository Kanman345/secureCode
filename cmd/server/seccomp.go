//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	seccomp "github.com/seccomp/libseccomp-golang"
)

// seccompKillOnViolation picks the default action for any syscall not on the
// whitelist. false (SCMP_ACT_ERRNO -> EPERM) is the safer choice while the
// whitelist is still being discovered: a blocked syscall shows up as a normal
// Python errno/exception instead of a silent SIGSYS kill with no diagnostic.
// Flip to true only after the whitelist below has been verified with strace
// against real submissions on the target Linux VM -- see README Phase 7.
const seccompKillOnViolation = false

// seccompWhitelist is a starting-point set of syscalls a bare CPython 3
// interpreter needs to start up and run typical algorithmic submissions
// (print, loops, math/string ops, in-jail file I/O). It is derived from
// well-documented CPython syscall usage, NOT yet verified with strace against
// this project's actual jail-template Python build -- that verification can
// only happen on Linux (see the Multipass checklist in README Phase 7).
// Deliberately excluded: clone/clone3/fork/vfork/execve/execveat (submissions
// are single-threaded algorithmic solutions with no legitimate need to spawn
// processes or threads -- this makes fork-bomb containment redundant with
// Phase 3's cgroup pids.max), ptrace, mount/umount2, socket/connect/bind (see
// README for the full rationale).
var seccompWhitelist = []string{
	// memory management
	"mmap", "munmap", "mprotect", "brk", "mremap", "madvise",

	// process/thread bookkeeping -- NOT clone/fork/execve, see doc comment above
	"exit", "exit_group", "gettid", "getpid", "getppid",
	"set_tid_address", "set_robust_list", "rseq",
	"sched_getaffinity", "sched_yield", "prlimit64",
	// uid/gid queries -- CPython's startup checks these unconditionally
	// (confirmed via strace on the target VM, 2026-07-20); purely
	// informational, can't be used to change privilege or read/write anything.
	"getuid", "geteuid", "getgid", "getegid",

	// signals
	"rt_sigaction", "rt_sigprocmask", "rt_sigreturn", "sigaltstack", "tgkill",

	// file I/O (within the jail only -- filesystem is chroot'd)
	"read", "write", "readv", "writev", "pread64", "pwrite64",
	"openat", "close", "close_range", "lseek", "dup", "dup2", "dup3", "fcntl",
	"ioctl", "getdents64", "readlink", "readlinkat", "getcwd",
	"fstat", "newfstatat", "statx", "access", "faccessat", "faccessat2",

	// time
	"clock_gettime", "gettimeofday", "clock_nanosleep", "nanosleep", "clock_getres",

	// misc interpreter startup
	"uname", "getrandom", "sysinfo", "prctl", "futex",
}

// archSpecificSyscalls are added only if the running kernel architecture
// actually defines them. GetSyscallFromName errors on a name that doesn't
// exist for the native arch -- e.g. arch_prctl (x86_64 TLS setup) has no
// arm64 equivalent, and most Multipass VMs on Apple Silicon are arm64.
var archSpecificSyscalls = []string{"arch_prctl"}

func buildSeccompFilter() (*seccomp.ScmpFilter, error) {
	action := seccomp.ActErrno.SetReturnCode(int16(syscall.EPERM))
	if seccompKillOnViolation {
		action = seccomp.ActKill
	}

	filter, err := seccomp.NewFilter(action)
	if err != nil {
		return nil, fmt.Errorf("new filter: %w", err)
	}

	for _, name := range append(append([]string{}, seccompWhitelist...), archSpecificSyscalls...) {
		id, err := seccomp.GetSyscallFromName(name)
		if err != nil {
			// Not defined on this architecture -- skip rather than fail the
			// whole filter (expected for e.g. arch_prctl on arm64).
			continue
		}
		if err := filter.AddRule(id, seccomp.ActAllow); err != nil {
			return nil, fmt.Errorf("add rule for %s: %w", name, err)
		}
	}

	return filter, nil
}

// runSandboxInit is the entry point for the re-exec'd child, invoked as
// `<server-binary> __sandbox_init__ <jailPath> <solutionPath>` from
// worker.go. It runs inside the mount/PID/net namespaces the outer
// exec.Command already created via Cloneflags, but BEFORE chroot: chroot
// happens here, manually, after mounting /proc but before the seccomp filter
// is loaded and python3 is exec'd.
//
// This ordering is deliberate:
//   - mount() itself must not be seccomp-filtered, so it has to happen before
//     the filter is loaded -- it runs as this trusted re-exec'd process, not
//     as the untrusted submission.
//   - Applying chroot/seccomp here, in a freshly exec'd single-threaded
//     process, avoids the classic unsafe pattern of calling them from a raw
//     fork() inside a multithreaded Go runtime (which os/exec deliberately
//     does not expose). Because Cloneflags already forked+exec'd this binary
//     as a distinct process image, we get an equivalent "post-fork, pre-exec"
//     hook safely.
//   - A seccomp filter loaded here persists across the final syscall.Exec
//     into python3, so it's enforced for the entire lifetime of the
//     submission's process without ever having restricted our own setup code.
func runSandboxInit(jailPath, solutionPath string) {
	fail := func(step string, err error) {
		fmt.Fprintf(os.Stderr, "sandbox-init: %s: %v\n", step, err)
		os.Exit(1)
	}

	if err := syscall.Mount("proc", jailPath+"/proc", "proc", 0, ""); err != nil {
		fail("mount proc", err)
	}

	if err := syscall.Chroot(jailPath); err != nil {
		fail("chroot", err)
	}
	if err := syscall.Chdir("/tmp"); err != nil {
		fail("chdir", err)
	}

	filter, err := buildSeccompFilter()
	if err != nil {
		fail("build seccomp filter", err)
	}
	defer filter.Release()

	if err := filter.Load(); err != nil {
		fail("load seccomp filter", err)
	}

	// Resolved after chroot so it respects the jail's own PATH/filesystem,
	// same as the plain `python3` PATH lookup this replaces.
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		fail("find python3", err)
	}

	if err := syscall.Exec(pythonPath, []string{"python3", solutionPath}, os.Environ()); err != nil {
		fail("exec python3", err)
	}
}
