package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func successResponse(data interface{}) {
	response := SuccessResponse{
		Version: "1.0",
		Data:    data,
	}
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		fmt.Printf(`{"error":{"code":110,"type":"json_error","message":"Failed to marshal response"}}` + "\n")
		os.Exit(110)
	}
	fmt.Println(string(jsonBytes))
}

func errorResponse(code int, errorType string, message string, recoverable bool) {
	response := ErrorResponse{
		Error: ErrorDetail{
			Code:        code,
			Type:        errorType,
			Message:     message,
			Recoverable: recoverable,
		},
	}
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		fmt.Printf(`{"error":{"code":110,"type":"json_error","message":"Failed to marshal error response"}}` + "\n")
		os.Exit(110)
	}
	fmt.Println(string(jsonBytes))
}
