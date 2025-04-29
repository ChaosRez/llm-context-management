package main

import (
	"bufio"
	"fmt"
	log "github.com/sirupsen/logrus"
	Scenario "llm-context-management/internal/app/scenario"
	SessionManager "llm-context-management/internal/app/session_manager"
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

	var sessionID string

	for _, message := range scen.Messages {
		log.Printf("Processing message: '%s'", message)
		// Create session if not exists
		if sessionID == "" {
			var err error
			sessionID, err = sessionManager.CreateSession(scen.UserID, 7)
			if err != nil {
				log.Fatalf("Failed to create session: %v", err)
			}
			log.Printf("Created a new Session ID: %s", sessionID)
		}
		// Add user message to session, set model name
		modelName := scen.ModelName
		_, err := sessionManager.AddMessage(sessionID, "user", message, nil, &modelName)
		if err != nil {
			log.Fatalf("Failed to add message: %v", err)
		}
		// Prepare context for LLM
		context, err := sessionManager.GetTextSessionContext(sessionID, 20)
		if err != nil {
			log.Fatalf("Failed to get session context: %v", err)
		}
		// Call LLM completion
		req := map[string]interface{}{
			"model":   scen.ModelName,
			"prompt":  message,
			"context": context,
		}
		resp, err := llamaService.Completion(req)
		if err != nil {
			log.Printf("Completion error: %v", err)
		}
		fmt.Printf("Response: %+v\n", resp["content"])
		// Add assistant response to session, set model name
		if resp != nil && resp["content"] != nil {
			assistantMsg := fmt.Sprintf("%v", resp["content"])
			sessionManager.AddMessage(sessionID, "assistant", assistantMsg, nil, &modelName)
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
