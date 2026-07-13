package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bytecodealliance/wasmtime-go/v3"
)

// ExecutePython runs Python code either in a secure Wasmtime sandbox or falls back to local execution.
func ExecutePython(userCode string, streamLog func(string)) string {
	wasmPath := filepath.Join("sandbox", "runtimes", "python-3.11.wasm")

	// Check if the precompiled Python WASM binary exists
	if _, err := os.Stat(wasmPath); err == nil {
		return executeWasmtime(wasmPath, userCode, streamLog)
	}

	// Fallback to local python3 execution
	return executeLocalPython(userCode, streamLog)
}

func executeWasmtime(wasmPath string, userCode string, streamLog func(string)) string {
	engine := wasmtime.NewEngine()
	store := wasmtime.NewStore(engine)

	wasiConfig := wasmtime.NewWasiConfig()

	// Create a temporary file to capture output
	tmpFile, err := os.CreateTemp("", "wasm-output-*")
	if err != nil {
		return fmt.Sprintf("System Error creating temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	wasiConfig.SetStdoutFile(tmpFile.Name())
	wasiConfig.SetStderrFile(tmpFile.Name())
	wasiConfig.SetArgv([]string{"python", "-c", userCode})
	store.SetWasi(wasiConfig)

	module, err := wasmtime.NewModuleFromFile(engine, wasmPath)
	if err != nil {
		return fmt.Sprintf("Error loading Python Wasm: %v", err)
	}

	linker := wasmtime.NewLinker(engine)
	if err := linker.DefineWasi(); err != nil {
		return fmt.Sprintf("Error defining WASI: %v", err)
	}

	instance, err := linker.Instantiate(store, module)
	if err != nil {
		return fmt.Sprintf("Error instantiating WASM: %v", err)
	}

	startFunc := instance.GetExport(store, "_start").Func()
	if startFunc == nil {
		return "Error: WASM module missing _start entry point"
	}

	// Timeout logic for Wasmtime execution
	type runResult struct {
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		_, err := startFunc.Call(store)
		done <- runResult{err: err}
	}()

	select {
	case res := <-done:
		err = res.err
	case <-time.After(2 * time.Second):
		return "Execution Timeout: Python function exceeded 2 seconds!"
	}

	// Read and stream captured output
	outputBytes, _ := os.ReadFile(tmpFile.Name())
	output := string(outputBytes)

	if err != nil {
		return fmt.Sprintf("Execution Error:\n%v\n\nCaptured Output:\n%s", err, output)
	}

	if streamLog != nil && len(output) > 0 {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				streamLog("Live Log: " + line)
			}
		}
	}

	if output == "" {
		return "Execution successful (No console output)"
	}

	return output
}

func executeLocalPython(userCode string, streamLog func(string)) string {
	// Create temporary script file to prevent shell injection/quoting issues
	tmpFile, err := os.CreateTemp("", "local-python-*.py")
	if err != nil {
		return fmt.Sprintf("System Error creating temp script: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(userCode); err != nil {
		tmpFile.Close()
		return fmt.Sprintf("System Error writing temp script: %v", err)
	}
	tmpFile.Close() // Close before execution so python can open it

	// Create context with 2-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Wrap execution in a bash script to set ulimit resource limits:
	// -v 131072 sets the virtual memory limit to 128MB
	// -f 1024 sets the maximum file size write to 512KB (1024 blocks of 512 bytes)
	// -u 15 sets the maximum number of processes/threads to 15
	cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("ulimit -v 131072 && ulimit -f 1024 && ulimit -u 15 && exec python3 %s", tmpFile.Name()))

	// Create a new process group to allow killing the entire group (including grandchildren) on timeout
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Tell CommandContext to kill the entire process group (-PID) when timing out
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// Capture output
	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)

	if ctx.Err() == context.DeadlineExceeded {
		return "Execution Timeout: Python function exceeded 2 seconds!"
	}

	if streamLog != nil && len(output) > 0 {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				streamLog("Live Log: " + line)
			}
		}
	}

	if err != nil {
		return fmt.Sprintf("Runtime Error: %v\n%s", err, output)
	}

	if output == "" {
		return "Execution successful (No console output)"
	}

	return output
}
