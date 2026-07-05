package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

type SubmitRequest struct {
	Code     string `json:"code"`
	Language string `json:"language"`
}

type SubmitResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
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

	solutionPath := filepath.Join(tempDir, "solution.py")
	if err := os.WriteFile(solutionPath, []byte(req.Code), 0644); err != nil {
		http.Error(w, "failed to write solution file", http.StatusInternalServerError)
		return
	}

	cmd := exec.Command("python3", "solution.py")
	cmd.Dir = tempDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			http.Error(w, "failed to execute code", http.StatusInternalServerError)
			return
		}
	}

	resp := SubmitResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/submit", submitHandler)
	fmt.Println("listening on :8080")
	http.ListenAndServe(":8080", nil)
}
