//go:build linux

package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const queueSize = 100
const queueWaitTimeout = 30 * time.Second

type Job struct {
	Code     string
	Language string
	Result   chan SubmitResponse
}

var jobQueue chan Job

func startWorkerPool(n int) {
	jobQueue = make(chan Job, queueSize)
	for i := 0; i < n; i++ {
		go worker(i)
	}
}

func worker(id int) {
	for job := range jobQueue {
		resp := executeJob(job.Code, job.Language)
		job.Result <- resp
	}
}

func errorResult(msg string) SubmitResponse {
	return SubmitResponse{Status: "internal_error", Stderr: msg}
}

func executeJob(code, language string) SubmitResponse {
	tempDir, err := os.MkdirTemp("", "sandbox-exec-*")
	if err != nil {
		return errorResult("failed to create temp dir")
	}
	defer os.RemoveAll(tempDir)

	jailPath := filepath.Join(tempDir, "jail")
	if err := exec.Command("cp", "-r", "/opt/jail-template", jailPath).Run(); err != nil {
		return errorResult("failed to set up jail")
	}

	solutionPath := filepath.Join(jailPath, "tmp", "solution.py")
	if err := os.WriteFile(solutionPath, []byte(code), 0644); err != nil {
		return errorResult("failed to write solution file")
	}

	cgroupPath := filepath.Join(cgroupRoot, filepath.Base(tempDir))
	if err := os.Mkdir(cgroupPath, 0755); err != nil {
		return errorResult("failed to create cgroup")
	}
	defer cleanupCgroup(cgroupPath)

	if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte("20"), 0644); err != nil {
		return errorResult("failed to set pids.max")
	}
	if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte("268435456"), 0644); err != nil {
		return errorResult("failed to set memory.max")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeLimit)
	defer cancel()

	start := time.Now()

	cmd := exec.CommandContext(ctx, "sh", "-c", "mount -t proc proc /proc && exec python3 /tmp/solution.py")
	cmd.Dir = "/tmp"
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:    true,
		Chroot:     jailPath,
		Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
	}

	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: maxOutputBytes, cancel: cancel}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: maxOutputBytes, cancel: cancel}

	if err := cmd.Start(); err != nil {
		return errorResult("failed to start process")
	}
	defer func() {
		if err := syscall.Unmount(filepath.Join(jailPath, "proc"), 0); err != nil {
			log.Printf("jail /proc unmount (likely already gone): %v", err)
		}
	}()

	pidBytes := []byte(strconv.Itoa(cmd.Process.Pid))
	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), pidBytes, 0644); err != nil {
		log.Printf("failed to add pid %d to cgroup: %v", cmd.Process.Pid, err)
	}

	runErr := cmd.Wait()

	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	elapsed := time.Since(start)
	log.Printf("execution took %v", elapsed)

	status := "success"
	exitCode := 0
	oomKilled := cgroupHitMemoryLimit(cgroupPath)

	if ctx.Err() == context.DeadlineExceeded {
		status = "time_limit_exceeded"
		log.Printf("submission killed after %v (limit %v): SIGKILL sent via context deadline", elapsed, defaultTimeLimit)
	} else if ctx.Err() == context.Canceled {
		status = "output_limit_exceeded"
	} else if strings.Contains(stderr.String(), "Resource temporarily unavailable") || strings.Contains(stderr.String(), "BlockingIOError") {
		status = "process_limit_exceeded"
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	} else if oomKilled {
		status = "memory_limit_exceeded"
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	} else if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			status = "runtime_error"
			exitCode = exitErr.ExitCode()
		} else {
			return errorResult("failed to execute code")
		}
	}

	return SubmitResponse{
		Status:    status,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  exitCode,
		ElapsedMs: elapsed.Milliseconds(),
	}
}

