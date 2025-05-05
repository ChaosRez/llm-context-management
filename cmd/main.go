package main

import (
	"bufio" // Needed for scenario mode
	"fmt"   // Needed for scenario mode
	log "github.com/sirupsen/logrus"
	Scenario "llm-context-management/internal/app/scenario" // Needed for scenario mode
	Server "llm-context-management/internal/app/server"
	SessionManager "llm-context-management/internal/app/session_manager"
	ContextStorage "llm-context-management/internal/pkg/context_storage"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
	"os" // Needed for scenario mode
	"strings"
)

// TODO fix the Payload keys for server
func main() {
	// --- Configuration ---
	const runServerMode = false // false to run the interactive scenario mode (file).
	const dbPath = "sessions.db"
	const sessionDurationDays = 1
	const llamaURL = "http://localhost:8080"
	const redisAddr = "localhost:6379"
	const serverListenAddr = ":8081"
	const scenarioFilePath = "testdata/example_ruby.yml" // only in scenario mode

	// --- Initialize common services ---
	sessionManager := SessionManager.NewSQLiteSessionManager(dbPath)
	llamaService := Llama.NewLlamaClient(llamaURL)
	redisContextStorage := ContextStorage.NewRedisContextStorage(redisAddr, "", 0)

	if runServerMode {
		// --- Server Mode ---
		log.Info("Starting in Server Mode...")
		srv := Server.NewServer(llamaService, sessionManager, redisContextStorage)
		log.Fatal(srv.Start(serverListenAddr))

	} else {
		// --- Interactive Scenario Mode ---
		log.Info("Starting in Interactive Scenario Mode...")

		// Load scenario from YAML
		scen, err := Scenario.LoadScenario(scenarioFilePath)
		if err != nil {
			log.Fatalf("Failed to load scenario '%s': %v", scenarioFilePath, err)
		}
		log.Infof("Loaded scenario: %s", scen.Name)
		log.Infof("Using model: %s", scen.ModelName)

		// Create a new session for the scenario
		sessionID, err := sessionManager.CreateSession(scen.Name, sessionDurationDays)
		if err != nil {
			log.Fatalf("Failed to create session: %v", err)
		}
		log.Infof("Created session ID: %s", sessionID)

		modelName := scen.ModelName // Capture model name for session messages

		// Interactive loop (from yaml)
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Choose context retrieval for the entire scenario (raw/tokenized): ")
		contextMethodInput, _ := reader.ReadString('\n')
		contextMethod := strings.TrimSpace(contextMethodInput) // Trim whitespace and newline

		if contextMethod != "raw" && contextMethod != "tokenized" {
			log.Fatalf("Invalid context retrieval method: %s. Choose 'raw' or 'tokenized'.", contextMethod)
		}
		log.Infof("Using '%s' context retrieval method for this scenario.", contextMethod)

		// Interactive loop (from yaml)
		for _, message := range scen.Messages {
			fmt.Printf("\nProcessing message: %s\n", message)

			var req map[string]interface{}
			var prompt string
			var textContext string
			var tokenizedContext []int
			var err error

			if contextMethod == "raw" {
				const rawHistoryLength = 20 // Or make configurable
				textContext, err = sessionManager.GetTextSessionContext(sessionID, rawHistoryLength)
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
			} else { // contextMethod == "tokenized"
				tokenizedContext, err = redisContextStorage.GetTokenizedSessionContext(sessionID)
				// Allow proceeding even if context doesn't exist yet (first message)
				if err != nil && err.Error() != "redis: nil" { // Check for specific redis nil error
					log.Warnf("Failed to get tokenized session context (proceeding without): %v", err)
				} else if err == nil {
					log.Debugf("Retrieved tokenized context for session %s", sessionID)
				} else {
					log.Infof("No existing tokenized context found for session %s, proceeding without.", sessionID)
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
			}

			resp, err := llamaService.Completion(req)
			if err != nil {
				log.Fatalf("Completion error: %v", err)
			}

			_, err = sessionManager.AddMessage(sessionID, "user", message, nil, &modelName)
			if err != nil {
				log.Errorf("Failed to add user message: %v", err)
			}

			// Process and add assistant response to session
			if resp != nil && resp["content"] != nil {
				assistantMsg := fmt.Sprintf("%v", resp["content"])
				fmt.Printf("Response: \n%s\n", assistantMsg)
				_, err = sessionManager.AddMessage(sessionID, "assistant", assistantMsg, nil, &modelName)
				if err != nil {
					log.Errorf("Failed to add assistant message: %v", err)
				}

				// Update tokenized context in Redis *after* adding both messages
				err = redisContextStorage.UpdateSessionContext(sessionID, sessionManager, llamaService)
				if err != nil {
					// Log warning instead of fatal, maybe allow continuation?
					log.Errorf("Failed to update tokenized session context: %v", err)
				} else {
					log.Infof("Updated tokenized context for session %s", sessionID)
				}
			} else {
				log.Warn("Received nil or empty response content.")
			}
		} // End of message loop

		// Prompt for session deletion (optional)
		if sessionID != "" {
			fmt.Print("Do you want to delete current session? (y/n): ")
			input, _ := reader.ReadString('\n')
			input = strings.ToLower(strings.TrimSpace(input)) // Normalize input
			switch input {
			case "y", "yes":
				if err := sessionManager.DeleteSession(sessionID); err != nil {
					log.Printf("Failed to delete session %s: %v", sessionID, err)
				} else {
					log.Printf("Deleted session %s.", sessionID)
				}
				if err := redisContextStorage.DeleteSessionContext(sessionID); err != nil {
					log.Printf("Failed to delete Redis context for session %s: %v", sessionID, err)
				} else {
					log.Printf("Deleted Redis context for session %s.", sessionID)
				}
			default:
				log.Printf("Session %s NOT deleted.", sessionID)
			}
		}
	} // End of mode switch
}
