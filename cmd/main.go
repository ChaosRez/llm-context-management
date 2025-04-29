package main

import (
	"fmt"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
)

func main() {
	client := Llama.NewLlamaClient("http://localhost:8080")

	compReq := map[string]interface{}{
		"model":       "Qwen1.5-0.5B-Chat-Q4_K_M:latest",
		"prompt":      "What language does people speak there",
		"context":     []int{27, 872, 29, 198, 9064, 374, 9856, 30, 198, 27, 77091, 29, 198, 258, 4505, 13},
		"temperature": 0,
		"seed":        123,
		"stream":      false,
	}
	comp, err := client.Completion(compReq)
	fmt.Println("Completion:", comp, "err:", err)
}
