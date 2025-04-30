package main

import (
	"bufio"
	"fmt"
	log "github.com/sirupsen/logrus"
	Scenario "llm-context-management/internal/app/scenario"
	SessionManager "llm-context-management/internal/app/session_manager"
	ContextStorage "llm-context-management/internal/pkg/context_storage"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
	"os"
)

func main() {
	// Load scenario from YAML
	scen, err := Scenario.LoadScenario("testdata/example_ruby.yml")
	if err != nil {
		log.Fatalf("Failed to load scenario: %v", err)
	}

	// Initialize services
	sessionManager := SessionManager.NewSQLiteSessionManager("sessions.db")
	llamaService := Llama.NewLlamaClient("http://localhost:8080")
	redisContextStorage := ContextStorage.NewRedisContextStorage("localhost:6379", "", 0)

	// Prompt user to choose context retrieval method
	input := "tokenized" // "raw" or "tokenized"
	log.Infof("running in '%s' mode", input)

	var sessionID string

	for _, message := range scen.Messages {
		log.Printf("Processing message: '%s'", message)
		if sessionID == "" {
			var err error
			sessionID, err = sessionManager.CreateSession(scen.UserID, 7)
			if err != nil {
				log.Fatalf("Failed to create session: %v", err)
			}
			log.Printf("Created a new Session ID: %s", sessionID)
		}
		modelName := scen.ModelName

		var (
			tokenizedContext []int
			textContext      string
			prompt           string
			req              map[string]interface{}
		)

		if input == "raw" {
			textContext, err = sessionManager.GetTextSessionContext(sessionID, 20)
			if err != nil {
				log.Fatalf("Failed to get raw session context: %v", err)
			}
			prompt = textContext + "<|im_start|>user\n" + message + "<|im_end|>\n"
			req = map[string]interface{}{
				"model":       scen.ModelName,
				"prompt":      prompt,
				"temperature": 0,
				"seed":        123,
				"stream":      false,
			}
		} else if input == "tokenized" {
			tokenizedContext, err = redisContextStorage.GetTokenizedSessionContext(sessionID)
			if err != nil {
				log.Fatalf("Failed to get tokenized session context: %v", err)
			}
			prompt = message
			req = map[string]interface{}{
				"model":       scen.ModelName,
				"prompt":      prompt,
				"temperature": 0,
				"seed":        123,
				"stream":      false,
			}
			if tokenizedContext != nil {
				req["context"] = tokenizedContext
			}
		} else {
			log.Fatalf("Invalid context retrieval method: %s", input)
		}

		resp, err := llamaService.Completion(req)
		if err != nil {
			log.Fatalf("Completion error: %v", err)
		}
		// Frst, add prompt message to session
		sessionManager.AddMessage(sessionID, "user", message, nil, &modelName)

		// Then, add response to the session
		fmt.Printf("Response: \n%+v\n", resp["content"])
		if resp != nil && resp["content"] != nil {
			assistantMsg := fmt.Sprintf("%v", resp["content"])
			sessionManager.AddMessage(sessionID, "assistant", assistantMsg, nil, &modelName)
			err = redisContextStorage.UpdateSessionContext(sessionID, sessionManager, llamaService)
			if err != nil {
				log.Fatalf("Failed to update session context: %v", err)
			}
		}
	}

	// Prompt for session deletion
	if sessionID != "" {
		fmt.Print("Do you want to delete current session? (y/n): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = string([]byte(input))
		switch s := input; s == "y\n" || s == "\n" || s == "yes\n" || s == "yy\n" {
		case true:
			if err := sessionManager.DeleteSession(sessionID); err != nil {
				log.Printf("Failed to delete session: %v", err)
			} else {
				log.Printf("Deleted session %s.", sessionID)
			}
		default:
			log.Printf("Session %s NOT deleted.", sessionID)
		}
	}
}
