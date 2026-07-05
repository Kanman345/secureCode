package main

import (
	"bytes" //stores outpur from programs
	"fmt"
	"os/exec" //runs external programs
	"time"
)

func main() {
	// --- Part 1: run a quick command, capture stdout/stderr separately, check exit code ---
	fmt.Println("=== Part 1: hello world ===")

	cmd := exec.Command("python3", "-c", "print('hello')")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// err was something other than a nonzero exit, e.g. python3 not found
			fmt.Println("failed to start command:", err)
			return
		}
	}

	fmt.Printf("stdout: %q\n", stdout.String())
	fmt.Printf("stderr: %q\n", stderr.String())
	fmt.Printf("exit code: %d\n", exitCode)

	// --- Part 2: start a long-running command and kill it early ---
	fmt.Println("\n=== Part 2: kill a running process ===")

	longCmd := exec.Command("python3", "-c", "import time; time.sleep(30)")

	start := time.Now()
	if err := longCmd.Start(); err != nil {
		fmt.Println("failed to start:", err)
		return
	}
	fmt.Println("started long-running process, pid:", longCmd.Process.Pid)

	// simulate "this submission has run too long" after 1 second
	time.Sleep(1 * time.Second)

	if err := longCmd.Process.Kill(); err != nil {
		fmt.Println("failed to kill:", err)
		return
	}
	fmt.Println("sent kill signal")

	// Wait() reaps the process and tells us how it actually ended
	waitErr := longCmd.Wait()
	elapsed := time.Since(start)

	fmt.Printf("process ended after %v (should be ~1s, not ~30s)\n", elapsed)
	fmt.Println("wait error (expected: signal: killed):", waitErr)
}

