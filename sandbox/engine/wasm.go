package engine

import (
	"fmt"
	"os"

	"github.com/bytecodealliance/wasmtime-go/v3"
)

// ExecutePythonWasm runs Python code inside a secure Wasmtime sandbox
func ExecutePythonWasm(userCode string) string {
	// 1. Initialize the Wasmtime Engine and Store (The Sandbox)
	engine := wasmtime.NewEngine()
	store := wasmtime.NewStore(engine)

	// 2. Configure the Virtual OS (WASI)
	// We need WASI so the Python interpreter can print to 'stdout'
	wasiConfig := wasmtime.NewWasiConfig()

	// Create a temporary file to capture the Python output securely
	tmpFile, _ := os.CreateTemp("", "wasm-output-*")
	defer os.Remove(tmpFile.Name()) // Clean up after execution

	wasiConfig.SetStdoutFile(tmpFile.Name())
	wasiConfig.SetStderrFile(tmpFile.Name())

	// Pass the user's code as an argument to the Python interpreter (e.g., `python -c "print('hello')"`)
	wasiConfig.SetArgv([]string{"python", "-c", userCode})
	store.SetWasi(wasiConfig)

	// 3. Load the pre-compiled Python Wasm Interpreter
	// (Assuming you placed python.wasm in your project root or runtimes folder)
	module, err := wasmtime.NewModuleFromFile(engine, "sandbox/runtimes/python-3.11.wasm")
	if err != nil {
		return fmt.Sprintf("Error loading Python Wasm: %v", err)
	}

	// 4. Link WASI and Instantiate the Sandbox
	linker := wasmtime.NewLinker(engine)
	linker.DefineWasi()
	instance, _ := linker.Instantiate(store, module)

	// 5. Execute the Python Interpreter
	startFunc := instance.GetExport(store, "_start").Func()
	_, err = startFunc.Call(store)

	// 6. Read the captured output
	outputBytes, _ := os.ReadFile(tmpFile.Name())
	output := string(outputBytes)

	if err != nil {
		return fmt.Sprintf("Execution Error:\n%v\n\nCaptured Output:\n%s", err, output)
	}

	if output == "" {
		return "[Execution completed with no console output]"
	}

	return output
}
