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
	"path/filepath"
	"runtime"
	"strings"
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

	job := Job{
		Code:     req.Code,
		Language: req.Language,
		Result:   make(chan SubmitResponse, 1),
	}

	select {
	case jobQueue <- job:
		// accepted, fall through to wait for result
	default:
		http.Error(w, "server busy, try again later", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	select {
	case resp := <-job.Result:
		json.NewEncoder(w).Encode(resp)
	case <-time.After(queueWaitTimeout):
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(SubmitResponse{Status: "queue_timeout"})
	}
}

func main() {
	setupCgroupRoot()

	numWorkers := runtime.NumCPU()
	if numWorkers < 4 {
		numWorkers = 4
	}
	startWorkerPool(numWorkers)
	log.Printf("worker pool started: %d workers, queue size %d", numWorkers, queueSize)

	http.HandleFunc("/submit", submitHandler)
	fmt.Println("listening on :8080")
	http.ListenAndServe(":8080", nil)
}

