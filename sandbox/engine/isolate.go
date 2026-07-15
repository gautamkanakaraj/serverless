package engine

import (
    "fmt"
    "strings"
    "time"

    "github.com/dop251/goja"
)

// ExecuteJS compiles and executes JS code. If a global 'handler' function is defined,
// it invokes it passing the event object. Returns captured logs, function return value, and error.
func ExecuteJS(code string, event map[string]interface{}, streamLog func(string)) (string, string, error) {
    vm := goja.New()
    vm.SetMaxCallStackSize(250) // Restrict stack depth to prevent stack overflow OOM
    var outputBuilder strings.Builder

    // Override console.log to capture stdout
    console := vm.NewObject()
    console.Set("log", func(call goja.FunctionCall) goja.Value {
        var logLine string
        for i, arg := range call.Arguments {
            if i > 0 {
                logLine += " "
            }
            logLine += fmt.Sprintf("%v", arg.Export())
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

    // Execute the raw code in the isolated VM to load function definitions
    _, err := vm.RunString(code)
    if err != nil {
        if strings.Contains(err.Error(), "Execution Timeout") {
            return outputBuilder.String(), "Execution Timeout: Function exceeded 2 seconds!", err
        }
        return outputBuilder.String(), fmt.Sprintf("Runtime Error: %v", err), err
    }

    // Check if the handler function exists in the JS context
    handlerVal := vm.Get("handler")
    if handlerVal != nil {
        if handlerFunc, ok := goja.AssertFunction(handlerVal); ok {
            // Convert Go map to Goja Value
            eventVal := vm.ToValue(event)
            
            // Invoke the handler function with the event parameter
            resVal, err := handlerFunc(goja.Undefined(), eventVal)
            if err != nil {
                return outputBuilder.String(), fmt.Sprintf("Handler Runtime Error: %v", err), err
            }
            
            // Return result serialized to JSON if it's an object/map, otherwise as string
            var resultStr string
            resExport := resVal.Export()
            if resExport != nil {
                switch resExport.(type) {
                case string:
                    resultStr = resVal.String()
                case map[string]interface{}, []interface{}:
                    // Serialize object returns to JSON
                    importJson, err := vm.RunString("JSON.stringify")
                    if err == nil {
                        if stringify, ok := goja.AssertFunction(importJson); ok {
                            strVal, err := stringify(goja.Undefined(), resVal)
                            if err == nil {
                                resultStr = strVal.String()
                            }
                        }
                    }
                    if resultStr == "" {
                        resultStr = fmt.Sprintf("%v", resExport)
                    }
                default:
                    resultStr = fmt.Sprintf("%v", resExport)
                }
            }
            return outputBuilder.String(), resultStr, nil
        }
    }

    // Fallback: If no handler function is defined, return the console output as the body
    return outputBuilder.String(), outputBuilder.String(), nil
}