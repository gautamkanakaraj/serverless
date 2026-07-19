package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	// Expose fetch global function to Goja
	vm.Set("fetch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("fetch requires at least a URL string"))
		}
		targetURL := call.Arguments[0].String()
		options := make(map[string]interface{})
		if len(call.Arguments) > 1 {
			if optsObj, ok := call.Arguments[1].Export().(map[string]interface{}); ok {
				options = optsObj
			}
		}

		resp, err := gojaFetch(vm, targetURL, options)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("fetch error: %v", err)))
		}

		respObj := vm.NewObject()
		respObj.Set("status", resp.Status)
		respObj.Set("statusText", resp.StatusText)
		respObj.Set("headers", resp.Headers)
		respObj.Set("text", func(c goja.FunctionCall) goja.Value {
			return vm.ToValue(resp.bodyText)
		})
		respObj.Set("json", func(c goja.FunctionCall) goja.Value {
			var parsedVal interface{}
			err := json.Unmarshal([]byte(resp.bodyText), &parsedVal)
			if err != nil {
				panic(vm.ToValue(fmt.Sprintf("failed to parse JSON response: %v", err)))
			}
			return vm.ToValue(parsedVal)
		})

		return respObj
	})

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

type fetchResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	bodyText   string
}

func gojaFetch(vm *goja.Runtime, targetURL string, options map[string]interface{}) (*fetchResponse, error) {
	method := "GET"
	if m, ok := options["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	var bodyReader io.Reader
	if b, ok := options["body"].(string); ok && b != "" {
		bodyReader = bytes.NewReader([]byte(b))
	}

	req, err := http.NewRequest(method, targetURL, bodyReader)
	if err != nil {
		return nil, err
	}

	if headers, ok := options["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if strVal, ok := v.(string); ok {
				req.Header.Set(k, strVal)
			}
		}
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read limited response body (max 512KB)
	limitReader := io.LimitReader(resp.Body, 512*1024)
	bodyBytes, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, err
	}

	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	return &fetchResponse{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    respHeaders,
		bodyText:   string(bodyBytes),
	}, nil
}