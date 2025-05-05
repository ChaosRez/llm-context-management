package session_manager

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type SQLiteSessionManager struct {
	dbPath string
}

func NewSQLiteSessionManager(dbPath string) *SQLiteSessionManager {
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
	db, err := mgr.open()
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Users table
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		user_id TEXT PRIMARY KEY,
		created_at INTEGER,
		last_active INTEGER,
		metadata TEXT
	)`)

	// Sessions table
	db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		user_id TEXT,
		created_at INTEGER,
		last_active INTEGER,
		expires_at INTEGER,
		FOREIGN KEY (user_id) REFERENCES users(user_id)
	)`)

	// Messages table
	db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		message_id TEXT PRIMARY KEY,
		session_id TEXT,
		role TEXT,
		content TEXT,
		tokens TEXT,
		model TEXT,
		timestamp INTEGER,
		FOREIGN KEY (session_id) REFERENCES sessions(session_id)
	)`)

	// Indices
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)`)
}

func (mgr *SQLiteSessionManager) CreateUser(userID string, metadata map[string]interface{}) (string, error) {
	db, err := mgr.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	if userID == "" {
		userID = uuid.NewString()
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
	//if sessionDurationDays == 0 {
	//	sessionDurationDays = 7
	//}
	sessionID := uuid.NewString()
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
		_, err = mgr.CreateUser(userID, nil)
		if err != nil {
			return "", err
		}
	} else if err != nil && err != sql.ErrNoRows {
		return "", err
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
	db.Exec("UPDATE sessions SET last_active = ? WHERE session_id = ?", now, sessionID)
	db.Exec("UPDATE users SET last_active = ? WHERE user_id = ?", now, userID)

	// Add the message
	messageID := uuid.NewString()
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
		var mid, role, content, tokens sql.NullString
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
	db, err := mgr.open()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("DELETE FROM messages WHERE session_id = ?", sessionID)
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM sessions WHERE session_id = ?", sessionID)
	return err
}

func (mgr *SQLiteSessionManager) CleanupExpiredSessions() (int, error) {
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
	for _, sid := range expired {
		db.Exec("DELETE FROM messages WHERE session_id = ?", sid)
	}
	_, err = db.Exec("DELETE FROM sessions WHERE expires_at < ?", now)
	if err != nil {
		return 0, err
	}
	return len(expired), nil
}
