package session_manager

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

type SQLiteSessionManager struct {
	dbPath string
}

func NewSQLiteSessionManager(dbPath string) *SQLiteSessionManager {
	startTime := time.Now()
	defer func() {
		log.Debugf("NewSQLiteSessionManager took %v", time.Since(startTime))
	}()
	if dbPath == "" {
		dbPath = "sessions.db"
	}
	mgr := &SQLiteSessionManager{dbPath: dbPath}
	mgr.initializeDB()
	return mgr
}

func (mgr *SQLiteSessionManager) open() (*sql.DB, error) {
	return sql.Open("sqlite3", mgr.dbPath)
}

func (mgr *SQLiteSessionManager) initializeDB() {
	startTime := time.Now()
	defer func() {
		log.Debugf("initializeDB took %v", time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Users table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users (
		user_id TEXT PRIMARY KEY,
		created_at INTEGER,
		last_active INTEGER,
		metadata TEXT
	)`)
	if err != nil {
		log.Errorf("Failed to create users table: %v", err)
	}

	// Sessions table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		user_id TEXT,
		created_at INTEGER,
		last_active INTEGER,
		expires_at INTEGER,
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	)`)
	if err != nil {
		log.Errorf("Failed to create sessions table: %v", err)
	}

	// Messages table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		message_id TEXT PRIMARY KEY,
		session_id TEXT,
		role TEXT,
		content TEXT,
		tokens TEXT,
		model TEXT,
		timestamp INTEGER,
		FOREIGN KEY (session_id) REFERENCES sessions(session_id)
	)`)
	if err != nil {
		log.Errorf("Failed to create messages table: %v", err)
	}

	// Indices
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`)
	if err != nil {
		log.Errorf("Failed to create index idx_sessions_user_id: %v", err)
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)`)
	if err != nil {
		log.Errorf("Failed to create index idx_messages_session_id: %v", err)
	}
}

func (mgr *SQLiteSessionManager) CreateUser(userID string, metadata map[string]interface{}) (string, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("CreateUser for userID '%s' took %v", userID, time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	if userID == "" {
		userID = mgr.generateShortID()
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metaBytes, _ := json.Marshal(metadata)
	now := time.Now().Unix()

	_, err = db.Exec(
		"INSERT OR IGNORE INTO users (user_id, created_at, last_active, metadata) VALUES (?, ?, ?, ?)",
		userID, now, now, string(metaBytes),
	)
	if err != nil {
		return "", err
	}
	return userID, nil
}

func (mgr *SQLiteSessionManager) CreateSession(userID string, sessionDurationDays int) (string, error) {
	startTime := time.Now()
	var sessionID string // Declare sessionID here to use in defer
	defer func() {
		log.Debugf("CreateSession for userID '%s', sessionID '%s' took %v", userID, sessionID, time.Since(startTime))
	}()
	//if sessionDurationDays == 0 {
	//	sessionDurationDays = 7
	//}
	sessionID = mgr.generateShortID()
	now := time.Now().Unix()
	expiresAt := now + int64(sessionDurationDays*24*60*60)

	db, err := mgr.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	// Ensure user exists
	var exists int
	err = db.QueryRow("SELECT 1 FROM users WHERE user_id = ?", userID).Scan(&exists)
	if err == sql.ErrNoRows {
		if _, errUser := mgr.CreateUser(userID, nil); errUser != nil {
			return "", errUser // Return specific error from CreateUser
		}
	} else if err != nil {
		return "", err // Return other query errors
	}

	_, err = db.Exec(
		"INSERT INTO sessions (session_id, user_id, created_at, last_active, expires_at) VALUES (?, ?, ?, ?, ?)",
		sessionID, userID, now, now, expiresAt,
	)
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

type SessionInfo struct {
	SessionID  string `json:"session_id"`
	CreatedAt  string `json:"created_at"`
	LastActive string `json:"last_active"`
	ExpiresAt  string `json:"expires_at"`
}

func (mgr *SQLiteSessionManager) GetUserSessions(userID string) ([]SessionInfo, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("GetUserSessions for userID '%s' took %v", userID, time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT session_id, created_at, last_active, expires_at FROM sessions WHERE user_id = ? ORDER BY last_active DESC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var sid string
		var created, last, expires int64
		if err := rows.Scan(&sid, &created, &last, &expires); err != nil {
			return nil, err
		}
		sessions = append(sessions, SessionInfo{
			SessionID:  sid,
			CreatedAt:  time.Unix(created, 0).Format(time.RFC3339),
			LastActive: time.Unix(last, 0).Format(time.RFC3339),
			ExpiresAt:  time.Unix(expires, 0).Format(time.RFC3339),
		})
	}
	return sessions, nil
}

func (mgr *SQLiteSessionManager) AddMessage(sessionID, role, content string, tokens interface{}, model *string) (string, error) {
	startTime := time.Now()
	var messageID string // Declare messageID here to use in defer
	defer func() {
		log.Debugf("AddMessage for sessionID '%s', messageID '%s' took %v", sessionID, messageID, time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	now := time.Now().Unix()
	var userID string
	var expiresAt int64
	err = db.QueryRow(
		"SELECT user_id, expires_at FROM sessions WHERE session_id = ?",
		sessionID,
	).Scan(&userID, &expiresAt)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("Session %s not found", sessionID)
	} else if err != nil {
		return "", err
	}
	if now > expiresAt {
		return "", fmt.Errorf("Session %s has expired", sessionID)
	}

	// Update session and user last_active
	// timing them separately might be too verbose, but could be done if performance issues are suspected here.
	if _, err := db.Exec("UPDATE sessions SET last_active = ? WHERE session_id = ?", now, sessionID); err != nil {
		return "", fmt.Errorf("failed to update session last_active for sessionID %s: %v", sessionID, err)
	}
	if _, err := db.Exec("UPDATE users SET last_active = ? WHERE user_id = ?", now, userID); err != nil {
		return "", fmt.Errorf("failed to update user last_active for userID %s: %v", userID, err)
	}

	// Add the message
	messageID = mgr.generateShortID()
	var tokensStr *string
	if tokens != nil {
		tokBytes, _ := json.Marshal(tokens)
		tokStr := string(tokBytes)
		tokensStr = &tokStr
	}
	// Note: timestamp is set to now + 7 days, as in the Python code
	timestamp := now + int64(7*24*60*60)
	_, err = db.Exec(
		"INSERT INTO messages (message_id, session_id, role, content, tokens, timestamp, model) VALUES (?, ?, ?, ?, ?, ?, ?)",
		messageID, sessionID, role, content, tokensStr, timestamp, model,
	)
	if err != nil {
		return "", err
	}
	return messageID, nil
}

type MessageInfo struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Tokens    string `json:"tokens"`
	Timestamp string `json:"timestamp"`
}

func (mgr *SQLiteSessionManager) GetSessionMessages(sessionID string, limit int) ([]MessageInfo, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("GetSessionMessages for sessionID '%s' with limit %d took %v", sessionID, limit, time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT message_id, role, content, tokens, timestamp FROM messages WHERE session_id = ? ORDER BY timestamp ASC LIMIT ?",
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []MessageInfo
	for rows.Next() {
		var mid, role, content, tokens sql.NullString // model is not selected
		var ts int64
		if err := rows.Scan(&mid, &role, &content, &tokens, &ts); err != nil {
			return nil, err
		}
		messages = append(messages, MessageInfo{
			MessageID: mid.String,
			Role:      role.String,
			Content:   content.String,
			Tokens:    tokens.String,
			Timestamp: time.Unix(ts, 0).Format(time.RFC3339),
		})
	}
	return messages, nil
}

// GetTextSessionContext returns formatted context for LLM inference using the specified format.
func (mgr *SQLiteSessionManager) GetTextSessionContext(sessionID string, maxMessages int) (string, error) {
	startTime := time.Now()
	defer func() {
		log.Debugf("GetTextSessionContext for sessionID '%s' with maxMessages %d took %v", sessionID, maxMessages, time.Since(startTime))
	}()
	messages, err := mgr.GetSessionMessages(sessionID, maxMessages)
	if err != nil {
		return "", err
	}
	formatted := ""
	for _, msg := range messages {
		formatted += fmt.Sprintf("<|im_start|>%s\n%s<|im_end|>\n", msg.Role, msg.Content)
	}
	// Add the final assistant start tag if needed by the model,
	// otherwise, remove or comment out the next line.
	//formatted += "<|im_start|>assistant\n"
	return formatted, nil
}

func (mgr *SQLiteSessionManager) DeleteSession(sessionID string) error {
	startTime := time.Now()
	defer func() {
		log.Debugf("DeleteSession for sessionID '%s' took %v", sessionID, time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		return err
	}
	defer db.Close()

	// TODO wrap these in a transaction for atomicity
	// For timing, we are timing the whole operation.
	if _, err := db.Exec("DELETE FROM messages WHERE session_id = ?", sessionID); err != nil {
		return fmt.Errorf("failed to delete messages for sessionID %s during DeleteSession: %v", sessionID, err) // Return early if deleting messages fails
	}
	_, err = db.Exec("DELETE FROM sessions WHERE session_id = ?", sessionID)
	return err
}

func (mgr *SQLiteSessionManager) CleanupExpiredSessions() (int, error) {
	startTime := time.Now()
	var sessionsDeleted int // To be used in the defer log
	defer func() {
		log.Debugf("CleanupExpiredSessions deleted %d sessions and took %v", sessionsDeleted, time.Since(startTime))
	}()
	db, err := mgr.open()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	now := time.Now().Unix()
	rows, err := db.Query("SELECT session_id FROM sessions WHERE expires_at < ?", now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var expired []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return 0, err
		}
		expired = append(expired, sid)
	}

	sessionsDeleted = len(expired) // Assign the count before potential early return

	// It's good practice to wrap these in a transaction for atomicity
	// For timing, we are timing the whole operation.
	// If atomicity is required, start a transaction here.
	// tx, err := db.Begin()
	// if err != nil {
	// 	return 0, err
	// }
	// defer tx.Rollback() // Rollback if not committed

	for _, sid := range expired {
		// If using a transaction: _, err = tx.Exec(...)
		if _, err := db.Exec("DELETE FROM messages WHERE session_id = ?", sid); err != nil {
			// Log or return this specific error if needed

			// TODO when returning, make sure to rollback the transaction if used.
			return 0, fmt.Errorf("failed to delete messages for expired sessionID %s during cleanup: %v", sid, err)
		}
	}
	// If using a transaction: result, err = tx.Exec(...)
	_, err = db.Exec("DELETE FROM sessions WHERE expires_at < ?", now)
	if err != nil {
		// If using a transaction, tx.Rollback() would be called by defer
		return 0, err
	}

	// If using a transaction:
	// if err = tx.Commit(); err != nil {
	// 	return 0, err
	// }

	// The number of affected rows from the DELETE sessions query might be more accurate
	// if some sessions had no messages or if there's a desire to confirm the DB operation.
	// However, len(expired) reflects the number of sessions identified for deletion.
	// For the exact number of rows deleted by the second EXEC:
	// actualRowsDeleted, _ := result.RowsAffected()
	// sessionsDeleted = int(actualRowsDeleted) // Update sessionsDeleted if using this

	// sessionsDeleted is already set to len(expired)
	return sessionsDeleted, nil
}

// generateShortID creates a shorter, non-dash-separated unique ID
func (mgr *SQLiteSessionManager) generateShortID() string {
	// Generate 8 random bytes (will result in 16-char hex string)
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to uuid with dashes removed if random generation fails
		log.Warnf("Failed to generate random bytes: %v, falling back to uuid", err)
		return strings.ReplaceAll(uuid.NewString(), "-", "")[0:16]
	}

	// Convert to hexadecimal (no dashes or special chars)
	id := hex.EncodeToString(b)
	return id
}
