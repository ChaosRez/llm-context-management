package context_storage

import (
	SessionManager "llm-context-management/internal/app/session_manager"
	Llama "llm-context-management/internal/pkg/llama_wrapper"
)

// ContextStorage defines the interface for session context persistence.
type ContextStorage interface {
	GetTokenizedSessionContext(sessionID string) ([]int, error)
	UpdateSessionContext(sessionID string, sessionManager *SessionManager.SQLiteSessionManager, llamaService *Llama.LlamaClient) error
	DeleteSessionContext(sessionID string) error
	// IsNotFoundError checks if an error signifies that a context was not found (e.g., cache miss).
	// This helps differentiate between "not found" and other errors.
	IsNotFoundError(err error) bool
}
