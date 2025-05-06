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
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TODO fix the Payload keys for server
func main() {
	// --- Configuration ---
	const runServerMode = true // false to run the interactive scenario mode (file).
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
		loadScenStartTime := time.Now()
		scen, err := Scenario.LoadScenario(scenarioFilePath)
		if err != nil {
			log.Fatalf("Failed to load scenario '%s': %v", scenarioFilePath, err)
		}
		log.Infof("Scenario.LoadScenario took %v", time.Since(loadScenStartTime))
		log.Infof("Loaded scenario: %s", scen.Name)
		log.Infof("Using model: %s", scen.ModelName)

		// Create a new session for the scenario
		createSessStartTime := time.Now()
		sessionID, err := sessionManager.CreateSession(scen.UserID, sessionDurationDays)
		if err != nil {
			log.Fatalf("Failed to create session: %v", err)
		}
		log.Infof("sessionManager.CreateSession took %v", time.Since(createSessStartTime))
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
			var opStartTime time.Time

			if contextMethod == "raw" {
				const rawHistoryLength = 20 // Or make configurable
				opStartTime = time.Now()
				textContext, err = sessionManager.GetTextSessionContext(sessionID, rawHistoryLength)
				log.Debugf("sessionManager.GetTextSessionContext (raw) took %v", time.Since(opStartTime))
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
				opStartTime = time.Now()
				tokenizedContext, err = redisContextStorage.GetTokenizedSessionContext(sessionID)
				log.Infof("redisContextStorage.GetTokenizedSessionContext took %v", time.Since(opStartTime))
				// Allow proceeding even if context doesn't exist yet (first message)
				if err != nil && err.Error() != "redis: nil" { // Check for specific redis nil error
					log.Warnf("Failed to get tokenized session context (proceeding without): %v", err)
				} else if err == nil && tokenizedContext != nil {
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

			opStartTime = time.Now()
			resp, err := llamaService.Completion(req)
			log.Infof("llamaService.Completion took %v", time.Since(opStartTime))
			if err != nil {
				log.Fatalf("Completion error: %v", err)
			}

			opStartTime = time.Now()
			_, err = sessionManager.AddMessage(sessionID, "user", message, nil, &modelName)
			log.Infof("sessionManager.AddMessage (user) took %v", time.Since(opStartTime))
			if err != nil {
				log.Errorf("Failed to add user message: %v", err)
			}

			// Process and add assistant response to session
			if resp != nil && resp["content"] != nil {
				assistantMsg := fmt.Sprintf("%v", resp["content"])
				fmt.Printf("Response: \n%s\n", assistantMsg)
				opStartTime = time.Now()
				_, err = sessionManager.AddMessage(sessionID, "assistant", assistantMsg, nil, &modelName)
				log.Infof("sessionManager.AddMessage (assistant) took %v", time.Since(opStartTime))
				if err != nil {
					log.Errorf("Failed to add assistant message: %v", err)
				}

				// Update tokenized context in Redis *after* adding both messages
				opStartTime = time.Now()
				err = redisContextStorage.UpdateSessionContext(sessionID, sessionManager, llamaService)
				log.Infof("redisContextStorage.UpdateSessionContext took %v", time.Since(opStartTime))
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
				delSessStartTime := time.Now()
				if err := sessionManager.DeleteSession(sessionID); err != nil {
					log.Printf("Failed to delete session %s: %v", sessionID, err)
				} else {
					log.Debugf("sessionManager.DeleteSession took %v", time.Since(delSessStartTime))
					log.Printf("Deleted session %s.", sessionID)
				}
				delCtxStartTime := time.Now()
				if err := redisContextStorage.DeleteSessionContext(sessionID); err != nil {
					log.Printf("Failed to delete Redis context for session %s: %v", sessionID, err)
				} else {
					log.Debugf("redisContextStorage.DeleteSessionContext took %v", time.Since(delCtxStartTime))
					log.Printf("Deleted Redis context for session %s.", sessionID)
				}
			default:
				log.Printf("Session %s NOT deleted.", sessionID)
			}
		}
	} // End of mode switch
}

func init() {
	ll, err := log.ParseLevel("debug")
	if err != nil {
		ll = log.InfoLevel
	}
	log.SetLevel(ll)

	log.SetReportCaller(true)
	log.SetFormatter(&log.TextFormatter{
		TimestampFormat: "15:04:05.000",
		FullTimestamp:   false,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			_, file := filepath.Split(f.File)
			return "", fmt.Sprintf(" %s:%d", file, f.Line)
		},
	})
}
