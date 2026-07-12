//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type SubmitRequest struct {
	Code     string `json:"code"`
	Language string `json:"language"`
}

type SubmitResponse struct {
	Status    string `json:"status"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

const defaultTimeLimit = 5 * time.Second
const cgroupRoot = "/sys/fs/cgroup/sandbox-exec"
const maxOutputBytes = 1 << 20 // 1MB

func setupCgroupRoot() {
	if err := os.MkdirAll(cgroupRoot, 0755); err != nil {
		log.Fatalf("failed to create cgroup root: %v", err)
	}
	if err := os.WriteFile("/sys/fs/cgroup/cgroup.subtree_control", []byte("+memory +pids"), 0644); err != nil {
		log.Fatalf("failed to enable controllers at cgroup root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"), []byte("+memory +pids"), 0644); err != nil {
		log.Fatalf("failed to enable controllers on sandbox-exec: %v", err)
	}
}

func cgroupHitMemoryLimit(cgroupPath string) bool {
	data, err := os.ReadFile(filepath.Join(cgroupPath, "memory.events"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if count, ok := strings.CutPrefix(line, "oom_kill "); ok {
			return strings.TrimSpace(count) != "0"
		}
	}
	return false
}

func cleanupCgroup(cgroupPath string) {
	_ = os.WriteFile(filepath.Join(cgroupPath, "cgroup.kill"), []byte("1"), 0644)
	for i := 0; i < 10; i++ {
		if err := os.Remove(cgroupPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type limitedWriter struct {
	buf    *bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.cancel()
		return len(p), nil
	}
	if len(p) > remaining {
		w.buf.Write(p[:remaining])
		w.cancel()
		return len(p), nil
	}
	return w.buf.Write(p)
}


func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	tempDir, err := os.MkdirTemp("", "sandbox-exec-*")
	if err != nil {
		http.Error(w, "failed to create temp dir", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir)

	jailPath := filepath.Join(tempDir, "jail")
	if err := exec.Command("cp", "-r", "/opt/jail-template", jailPath).Run(); err != nil {
		http.Error(w, "failed to set up jail", http.StatusInternalServerError)
		return
	}

	solutionPath := filepath.Join(jailPath, "tmp", "solution.py")
	if err := os.WriteFile(solutionPath, []byte(req.Code), 0644); err != nil {
		http.Error(w, "failed to write solution file", http.StatusInternalServerError)
		return
	}

	cgroupPath := filepath.Join(cgroupRoot, filepath.Base(tempDir))
	if err := os.Mkdir(cgroupPath, 0755); err != nil {
		http.Error(w, "failed to create cgroup", http.StatusInternalServerError)
		return
	}
	defer cleanupCgroup(cgroupPath)

	if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte("20"), 0644); err != nil {
		http.Error(w, "failed to set pids.max", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte("268435456"), 0644); err != nil {
		http.Error(w, "failed to set memory.max", http.StatusInternalServerError)
		return
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
		http.Error(w, "failed to start process", http.StatusInternalServerError)
		return
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
			http.Error(w, "failed to execute code", http.StatusInternalServerError)
			return
		}
	}



	resp := SubmitResponse{
		Status:    status,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  exitCode,
		ElapsedMs: elapsed.Milliseconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	setupCgroupRoot()
	http.HandleFunc("/submit", submitHandler)
	fmt.Println("listening on :8080")
	http.ListenAndServe(":8080", nil)
}
