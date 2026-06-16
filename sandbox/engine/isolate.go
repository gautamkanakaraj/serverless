
package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// ExecuteJS runs the provided JavaScript code in an isolated, secure VM
func ExecuteJS(code string) string {
	// 1. Create a new isolated JavaScript Context
	vm := goja.New()
	var outputBuilder strings.Builder

	// 2. Redirect Output: Override console.log to capture stdout
	console := vm.NewObject()
	// Inside your ExecuteJS function...

    console.Set("log", func(call goja.FunctionCall) goja.Value {
    var logLine string
    for _, arg := range call.Arguments {
        logLine += fmt.Sprintf("%v ", arg.Export())
    }
    
    // 1. Keep saving it to the builder for the final HTTP response
    outputBuilder.WriteString(logLine + "\n")
    
    // 2. NEW: Blast it instantly to the frontend terminal!
    router.BroadcastLog("Live Log: " + logLine) 
    
    return goja.Undefined()
})
	vm.Set("console", console)

	// 3. Set Timeouts: Prevent infinite loops (Resource Guardrail)
	// If the code runs longer than 2 seconds, we kill the execution
	timer := time.AfterFunc(2*time.Second, func() {
		vm.Interrupt("Execution Timeout: Function exceeded 2 seconds!")
	})
	defer timer.Stop()

	// 4. Execute the raw code in the isolated VM
	_, err := vm.RunString(code)
	if err != nil {
		// Capture any syntax or runtime errors
		outputBuilder.WriteString(fmt.Sprintf("Runtime Error: %v\n", err))
	}

	// Return the captured output
	if outputBuilder.Len() == 0 {
		return "Execution successful (No console output)"
	}
	
	return outputBuilder.String()
}