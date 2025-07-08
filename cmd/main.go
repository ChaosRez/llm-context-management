package main

import (
	"bufio" // Needed for scenario mode
	"encoding/csv"
	"fmt" // Needed for scenario mode
	log "github.com/sirupsen/logrus"
	Scenario "llm-context-management/internal/app/scenario" // Needed for scenario mode
	Server "llm-context-management/internal/app/server"
	SessionManager "llm-context-management/internal/app/session_manager"
	ContextStorage "llm-context-management/internal/pkg/context_storage"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
	"os" // Needed for scenario mode
	"path/filepath"
	"runtime"
	"strconv" // for CSV writing (duration to ms)
	"strings"
	"time"
)

// TODO fix the Payload keys for server

// Helper function to write operation timing to CSV
func writeOperationToCsv(writer *csv.Writer, opActualStartTime time.Time, operationName string, duration time.Duration, contextMethod string, scenarioName string, sessionID string, requestSize int, promptChars int, contextTokens int, turn int, details string) {
	if writer == nil {
		log.Warnf("CSV writer not initialized when trying to log operation: %s", operationName)
		return
	}
	record := []string{
		opActualStartTime.Format("2006-01-02T15:04:05.000Z07:00"), // ISO8601 like timestamp for operation start
		operationName,
		strconv.FormatInt(duration.Milliseconds(), 10),
		contextMethod,
		scenarioName,
		sessionID,
		strconv.Itoa(requestSize),
		strconv.Itoa(promptChars),
		strconv.Itoa(contextTokens),
		strconv.Itoa(turn),
		details,
	}
	if err := writer.Write(record); err != nil {
		log.Errorf("Failed to write record to CSV for operation %s: %v", operationName, err)
	}
}

func main() {
	// --- Configuration ---
	const runServerMode = true // false to run the scenario mode (file).
	const dbPath = "sessions.db"
	const sessionDurationDays = 1
	const llamaURL = "http://localhost:8080"
	const redisAddr = "localhost:6379"
	// const redisAddr = "localhost:6379"
	const fredAddr = "141.23.28.210:9001" //"localhost:9001" // FIXME:
	const fredKeygroup = "qwen15test"     // NOTE: we isolate models's sessions by keygroup
	const fredCreateKeygroup = true       // Attempt to create keygroup if not exists
	const serverListenAddr = ":8081"
	const scenarioFilePath = "testdata/example_ruby.yml" // only in scenario mode
	const rawHistoryLength = 20

	// --- Initialize common services ---
	sessionManager := SessionManager.NewSQLiteSessionManager(dbPath)
	llamaService := Llama.NewLlamaClient(llamaURL)
	//redisContextStorage := ContextStorage.NewRedisContextStorage(redisAddr, "", 0)

	// Initialize FReDContextStorage
	fredContextStorage, err := ContextStorage.NewFReDContextStorage(fredAddr, fredKeygroup, fredCreateKeygroup)
	if err != nil {
		log.Fatalf("Failed to initialize FReDContextStorage: %v", err)
	}
	log.Info("Successfully initialized FReDContextStorage.")

	if runServerMode {
		// --- Server Mode ---
		log.Info("Starting in Server Mode...")
		srv := Server.NewServer(llamaService, sessionManager, fredContextStorage) // redisContextStorage
		defer srv.Stop()                                                          // Ensure cleanup on exit
		log.Fatal(srv.Start(serverListenAddr))

	} else {
		// --- Interactive Scenario Mode ---
		log.Info("Starting in Interactive Scenario Mode...")

		var csvFile *os.File
		var csvWriter *csv.Writer
		var errCsv error

		// Load scenario from YAML
		loadScenOpStartTime := time.Now()
		scen, errScenario := Scenario.LoadScenario(scenarioFilePath)
		if errScenario != nil {
			log.Fatalf("Failed to load scenario '%s': %v", scenarioFilePath, errScenario)
		}
		loadScenDuration := time.Since(loadScenOpStartTime)
		log.Infof("Scenario.LoadScenario took %v", loadScenDuration)
		log.Infof("Loaded scenario: %s", scen.Name)
		log.Infof("Using model: %s", scen.ModelName)

		// Create a new session for the scenario
		createSessOpStartTime := time.Now()
		sessionID, errSession := sessionManager.CreateSession(scen.UserID, sessionDurationDays)
		if errSession != nil {
			log.Fatalf("Failed to create session: %v", errSession)
		}
		createSessDuration := time.Since(createSessOpStartTime)
		log.Infof("sessionManager.CreateSession took %v", createSessDuration)
		log.Infof("Created session ID: %s", sessionID)

		// Interactive loop (from yaml)
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Choose context retrieval for the entire scenario (raw/tokenized): ")
		contextMethodInput, _ := reader.ReadString('\n')
		contextMethod := strings.TrimSpace(contextMethodInput) // Trim whitespace and newline

		if contextMethod != "raw" && contextMethod != "tokenized" {
			log.Fatalf("Invalid context retrieval method: %s. Choose 'raw' or 'tokenized'.", contextMethod)
		}
		log.Infof("Using '%s' context retrieval method for this scenario.", contextMethod)

		// Initialize CSV writer now that contextMethod and scen.Name are known
		safeScenarioName := strings.ReplaceAll(strings.ToLower(scen.Name), " ", "_")
		safeScenarioName = strings.ReplaceAll(safeScenarioName, "/", "_") // Basic sanitization
		safeContextMethod := strings.ReplaceAll(strings.ToLower(contextMethod), " ", "_")

		// Define the log directory
		logDir := "testdata/log/"
		// Ensure the log directory exists
		if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
			log.Fatalf("Failed to create log directory %s: %v", logDir, err)
		}

		csvFilename := filepath.Join(logDir, fmt.Sprintf("%s_%s_scenario_%s.csv",
			time.Now().Format("20060102_150405"),
			safeContextMethod,
			safeScenarioName))

		csvFile, errCsv = os.Create(csvFilename)
		if errCsv != nil {
			log.Fatalf("Failed to create CSV log file %s: %v", csvFilename, errCsv)
		}
		defer csvFile.Close()

		csvWriter = csv.NewWriter(csvFile)
		defer csvWriter.Flush()

		headers := []string{"Timestamp", "Operation", "DurationMs", "ContextMethod", "ScenarioName", "SessionID", "RequestSizeBytes", "PromptChars", "ContextTokens", "Turn", "Details"}
		if err := csvWriter.Write(headers); err != nil {
			log.Fatalf("Failed to write CSV header to %s: %v", csvFilename, err)
		}
		log.Infof("Logging operations to %s", csvFilename)

		// Write the previously captured timings
		writeOperationToCsv(csvWriter, loadScenOpStartTime, "Scenario.LoadScenario", loadScenDuration, contextMethod, scen.Name, "", -1, -1, -1, -1, fmt.Sprintf("File: %s", filepath.Base(scenarioFilePath)))
		writeOperationToCsv(csvWriter, createSessOpStartTime, "sessionManager.CreateSession", createSessDuration, contextMethod, scen.Name, sessionID, -1, -1, -1, -1, fmt.Sprintf("UserID: %s, DurationDays: %d", scen.UserID, sessionDurationDays))

		scenarioProcessingStartTime := time.Now() // Start timing after context method selection

		// Scenario loop (from yaml)
		var currentTokenizedContext []int // Declare here to persist across iterations for tokenized mode
		var currentRawMessages []ContextStorage.RawMessage
		var currentTurn int = 0

		for i, message := range scen.Messages {
			fmt.Printf("Processing message: %s\n", message)

			var req map[string]interface{}
			var prompt string
			var errCtx error
			var opStartTime time.Time
			var opDuration time.Duration

			if contextMethod == "raw" {
				opStartTime = time.Now()
				var fetchedMessages []ContextStorage.RawMessage
				var fredTurn int
				fetchedMessages, fredTurn, errCtx = fredContextStorage.GetRawSessionContext(sessionID)
				opDuration = time.Since(opStartTime)
				log.Debugf("fredContextStorage.GetRawSessionContext took %v", opDuration)
				writeOperationToCsv(csvWriter, opStartTime, "fredContextStorage.GetRawSessionContext", opDuration, contextMethod, scen.Name, sessionID, -1, -1, -1, currentTurn, fmt.Sprintf("MessageIndex: %d", i))

				if errCtx != nil && !fredContextStorage.IsNotFoundError(errCtx) {
					log.Fatalf("Failed to get raw session context: %v", errCtx)
				} else if errCtx == nil && fetchedMessages != nil {
					currentRawMessages = fetchedMessages
					currentTurn = fredTurn
					log.Debugf("Retrieved raw context for session %s, messages: %d, turn: %d", sessionID, len(currentRawMessages), currentTurn)
				} else {
					currentRawMessages = []ContextStorage.RawMessage{}
					currentTurn = 0
					log.Infof("No existing raw context found for session %s, starting fresh.", sessionID)
				}

				var textContextBuilder strings.Builder
				for _, msg := range currentRawMessages {
					textContextBuilder.WriteString(fmt.Sprintf("<|im_start|>%s\n%s<|im_end|>\n", msg.Role, msg.Content))
				}
				prompt = textContextBuilder.String() + "<|im_start|>user\n" + message + "<|im_end|>\n"

				req = map[string]interface{}{
					"model":       scen.ModelName,
					"prompt":      prompt,
					"temperature": 0,
					"seed":        123,
					"stream":      false,
				}
			} else { // contextMethod == "tokenized"
				opStartTime = time.Now()
				// GetTokenizedSessionContext is called only once at the beginning if currentTokenizedContext is nil (first message)
				if i == 0 {
					var fetchedTokens []int
					var fredTurn int
					fetchedTokens, fredTurn, errCtx = fredContextStorage.GetTokenizedSessionContext(sessionID)
					opDuration = time.Since(opStartTime)
					log.Infof("fredContextStorage.GetTokenizedSessionContext (initial) took %v", opDuration)
					writeOperationToCsv(csvWriter, opStartTime, "fredContextStorage.GetTokenizedSessionContext (initial)", opDuration, contextMethod, scen.Name, sessionID, -1, -1, -1, currentTurn, fmt.Sprintf("MessageIndex: %d", i))
					if errCtx != nil && !fredContextStorage.IsNotFoundError(errCtx) {
						log.Warnf("Failed to get tokenized session context (proceeding without): %v", errCtx)
						currentTokenizedContext = []int{} // Initialize to empty if error but not NotFound
						currentTurn = 0
					} else if errCtx == nil && fetchedTokens != nil {
						currentTokenizedContext = fetchedTokens
						currentTurn = fredTurn
						log.Debugf("Retrieved initial tokenized context for session %s, length: %d, turn: %d", sessionID, len(currentTokenizedContext), currentTurn)
					} else {
						currentTokenizedContext = []int{} // Initialize to empty if not found or nil
						currentTurn = 0
						log.Infof("No existing tokenized context found for session %s, starting fresh.", sessionID)
					}
				} else {
					// For subsequent messages, currentTokenizedContext already holds the context from previous iteration
					log.Debugf("Using existing tokenized context for session %s, length: %d, turn: %d", sessionID, len(currentTokenizedContext), currentTurn)
				}

				prompt = message // For tokenized mode, prompt is just the new user message
				req = map[string]interface{}{
					"model":       scen.ModelName,
					"prompt":      prompt,
					"temperature": 0,
					"seed":        123,
					"stream":      false,
				}
				if len(currentTokenizedContext) > 0 { // only add context if it's not empty
					req["context"] = currentTokenizedContext
				}
			}

			opStartTime = time.Now()
			resp, errCompletion := llamaService.Completion(req)
			opDuration = time.Since(opStartTime)
			log.Infof("llamaService.Completion took %v", opDuration)
			writeOperationToCsv(csvWriter, opStartTime, "llamaService.Completion", opDuration, contextMethod, scen.Name, sessionID, -1, len(prompt), len(currentTokenizedContext), currentTurn+1, fmt.Sprintf("MessageIndex: %d", i))
			if errCompletion != nil {
				log.Fatalf("Completion error: %v", errCompletion)
			}

			// Process and add assistant response to session
			if resp != nil && resp["content"] != nil {
				assistantMsg := fmt.Sprintf("%v", resp["content"])
				fmt.Printf("Response: \n%s\n", assistantMsg)
				if contextMethod == "raw" {
					// --- Construct new message history ---
					newHistory := append(currentRawMessages, ContextStorage.RawMessage{Role: "user", Content: message})
					if assistantMsg != "" {
						newHistory = append(newHistory, ContextStorage.RawMessage{Role: "assistant", Content: assistantMsg})
					}

					// --- Update raw context in FReD ---
					updateCtxOpStartTime := time.Now()
					errUpdateCtx := fredContextStorage.UpdateRawSessionContext(sessionID, newHistory, currentTurn+1)
					updateCtxOpDuration := time.Since(updateCtxOpStartTime)
					log.Infof("fredContextStorage.UpdateRawSessionContext took %v", updateCtxOpDuration)
					writeOperationToCsv(csvWriter, updateCtxOpStartTime, "fredContextStorage.UpdateRawSessionContext", updateCtxOpDuration, contextMethod, scen.Name, sessionID, -1, -1, len(newHistory), currentTurn+1, fmt.Sprintf("MessageIndex: %d", i))

					if errUpdateCtx != nil {
						log.Fatalf("Failed to update raw session context: %v", errUpdateCtx)
					} else {
						currentRawMessages = newHistory // Persist for next iteration
					}

					// --- Increment turn in SQLite ---
					opStartTime = time.Now()
					errIncrement := sessionManager.IncrementSessionTurn(sessionID)
					opDuration = time.Since(opStartTime)
					log.Infof("sessionManager.IncrementSessionTurn took %v", opDuration)
					writeOperationToCsv(csvWriter, opStartTime, "sessionManager.IncrementSessionTurn", opDuration, contextMethod, scen.Name, sessionID, -1, -1, -1, currentTurn, fmt.Sprintf("MessageIndex: %d, NewTurn: %d", i, currentTurn+1))
					if errIncrement != nil {
						log.Fatalf("Failed to increment turn: %v", errIncrement)
					}
					currentTurn++
				}

				// Update tokenized context in context store *after* adding both messages
				if contextMethod == "tokenized" {
					// Construct the text for the new interaction part
					// This format should match how the initial context was (or would have been) tokenized.
					// Assuming the format is: <|im_start|>user\nUSER_MSG<|im_end|>\n<|im_start|>assistant\nASSISTANT_MSG<|im_end|>\n
					newUserInteractionText := fmt.Sprintf("<|im_start|>user\n%s<|im_end|>\n<|im_start|>assistant\n%s<|im_end|>\n", message, assistantMsg)

					tokenizeNewOpStartTime := time.Now()
					newInteractionTokens, errTokenize := llamaService.Tokenize(newUserInteractionText)
					tokenizeNewOpDuration := time.Since(tokenizeNewOpStartTime)
					log.Infof("llamaService.Tokenize (new interaction) took %v", tokenizeNewOpDuration)
					writeOperationToCsv(csvWriter, tokenizeNewOpStartTime, "llamaService.Tokenize (new interaction)", tokenizeNewOpDuration, contextMethod, scen.Name, sessionID, -1, len(newUserInteractionText), -1, currentTurn+1, fmt.Sprintf("MessageIndex: %d", i))

					if errTokenize != nil {
						log.Errorf("Failed to tokenize new interaction for session %s: %v", sessionID, errTokenize)
						// Decide how to handle: skip update, clear cache, etc. For now, log and continue.
					} else {
						if currentTokenizedContext == nil { // Should have been initialized to []int{} earlier
							currentTokenizedContext = []int{}
						}
						// Append new tokens to the existing context // FIXME: bad templating?
						updatedFullTokenizedContext := append(currentTokenizedContext, newInteractionTokens...)

						updateCtxOpStartTime := time.Now()
						// Pass the complete, updated tokenized context to FReD
						errUpdateCtx := fredContextStorage.UpdateSessionContext(sessionID, updatedFullTokenizedContext, currentTurn+1)
						updateCtxOpDuration := time.Since(updateCtxOpStartTime)
						log.Infof("fredContextStorage.UpdateSessionContext took %v", updateCtxOpDuration)
						writeOperationToCsv(csvWriter, updateCtxOpStartTime, "fredContextStorage.UpdateSessionContext", updateCtxOpDuration, contextMethod, scen.Name, sessionID, -1, -1, len(updatedFullTokenizedContext), currentTurn+1, fmt.Sprintf("MessageIndex: %d", i))

						if errUpdateCtx != nil {
							log.Fatalf("Failed to update tokenized session context: %v", errUpdateCtx)
						} else {
							currentTokenizedContext = updatedFullTokenizedContext // Persist for next iteration
							currentTurn++
							log.Infof("Updated tokenized context for session %s, new total length: %d, new turn: %d", sessionID, len(currentTokenizedContext), currentTurn)
						}
					}
				}
			} else {
				log.Warn("Received nil or empty response content.")
			}
		} // End of message loop

		totalScenarioProcessingDuration := time.Since(scenarioProcessingStartTime)
		log.Infof("Total processing time for scenario '%s' using '%s' context method: %v", scen.Name, contextMethod, totalScenarioProcessingDuration)
		writeOperationToCsv(csvWriter, scenarioProcessingStartTime, "TotalScenarioProcessing", totalScenarioProcessingDuration, contextMethod, scen.Name, sessionID, -1, -1, -1, -1, fmt.Sprintf("MessageCount: %d", len(scen.Messages)))

		// Prompt for session deletion (optional)
		if sessionID != "" {
			fmt.Print("Do you want to delete current session? (y/n): ")
			input, _ := reader.ReadString('\n')
			input = strings.ToLower(strings.TrimSpace(input)) // Normalize input
			switch input {
			case "y", "yes":
				delSessStartTime := time.Now()
				if errDelSess := sessionManager.DeleteSession(sessionID); errDelSess != nil {
					log.Printf("Failed to delete session %s: %v", sessionID, errDelSess)
				} else {
					log.Debugf("sessionManager.DeleteSession took %v", time.Since(delSessStartTime))
					log.Printf("Deleted session %s.", sessionID)
				}
				delCtxStartTime := time.Now()
				if errDelCtx := fredContextStorage.DeleteSessionContext(sessionID); errDelCtx != nil {
					log.Printf("Failed to delete FReD context for session %s: %v", sessionID, errDelCtx)
				} else {
					log.Debugf("fredContextStorage.DeleteSessionContext took %v", time.Since(delCtxStartTime))
					log.Printf("Deleted FReD context for session %s.", sessionID)
				}
			default:
				log.Printf("Session %s NOT deleted.", sessionID)
			}
		}
	} // End of mode switch
}

func init() {
	ll, err := log.ParseLevel("info") // TODO: load from config
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
