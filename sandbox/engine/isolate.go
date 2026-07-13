package engine

import (
    "fmt"
    "strings"
    "time"

    "github.com/dop251/goja"
)

// ExecuteJS now accepts a streamLog callback to prevent Go import cycles!
func ExecuteJS(code string, streamLog func(string)) string {
    vm := goja.New()
    vm.SetMaxCallStackSize(250) // Restrict stack depth to prevent stack overflow OOM
    var outputBuilder strings.Builder

    // Override console.log to capture stdout
    console := vm.NewObject()
    console.Set("log", func(call goja.FunctionCall) goja.Value {
        var logLine string
        for _, arg := range call.Arguments {
            logLine += fmt.Sprintf("%v ", arg.Export())
        }
        
        // 1. Keep saving it to the builder for the final HTTP response
        outputBuilder.WriteString(logLine + "\n")
        
        // 2. Blast it instantly to the frontend using the callback!
        if streamLog != nil {
            streamLog("Live Log: " + logLine) 
        }
        
        return goja.Undefined()
    })
    vm.Set("console", console)

    // Set Timeouts: Prevent infinite loops
    timer := time.AfterFunc(2*time.Second, func() {
        vm.Interrupt("Execution Timeout: Function exceeded 2 seconds!")
    })
    defer timer.Stop()

    // Execute the raw code in the isolated VM
    _, err := vm.RunString(code)
    if err != nil {
        outputBuilder.WriteString(fmt.Sprintf("Runtime Error: %v\n", err))
    }

    // Return the captured output
    if outputBuilder.Len() == 0 {
        return "Execution successful (No console output)"
    }
    
    return outputBuilder.String()
}