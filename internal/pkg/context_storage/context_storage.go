package context_storage

// RawMessage defines the structure for a single message in raw context.
type RawMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ContextStorage defines the interface for session context persistence.
type ContextStorage interface {
	GetTokenizedSessionContext(sessionID string) ([]int, int, error)
	UpdateSessionContext(sessionID string, newFullTokenizedContext []int, newTurn int) error

	GetRawSessionContext(sessionID string) ([]RawMessage, int, error)
	UpdateRawSessionContext(sessionID string, newMessages []RawMessage, newTurn int) error

	DeleteSessionContext(sessionID string) error
	// IsNotFoundError checks if an error signifies that a context was not found (e.g., cache miss).
	// This helps differentiate between "not found" and other errors.
	IsNotFoundError(err error) bool
}
