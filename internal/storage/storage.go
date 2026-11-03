package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/vovaki-coger/al-royter/internal/models"
	"github.com/vovaki-coger/al-royter/internal/providers"
	"github.com/vovaki-coger/al-royter/internal/shared"
)

type Store struct {
	db     *sql.DB
	logger *zap.Logger
	mu     sync.Mutex
}

type DatabaseConfig struct {
	Type string
	Path string
}

func New(cfg DatabaseConfig) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", cfg.Path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	store := &Store{db: db, logger: zap.L().Named("storage")}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE,
			password_hash TEXT,
			master_key TEXT UNIQUE,
			nvidia_key TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS provider_keys (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			api_key TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE(user_id, provider)
		)`,
		`CREATE TABLE IF NOT EXISTS models (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			data TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			token TEXT UNIQUE NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateUser(email, passwordHash, masterKey, nvidiaKey string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateID()
	_, err := s.db.Exec(
		`INSERT INTO users (id, email, password_hash, master_key, nvidia_key) VALUES (?, ?, ?, ?, ?)`,
		id, email, passwordHash, masterKey, nvidiaKey,
	)
	return id, err
}

func (s *Store) GetUserByEmail(email string) (*shared.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT id, email, password_hash, master_key, nvidia_key, created_at FROM users WHERE email = ?`, email)
	return scanUser(row)
}

func (s *Store) GetUserByMasterKey(masterKey string) (*shared.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT id, email, password_hash, master_key, nvidia_key, created_at FROM users WHERE master_key = ?`, masterKey)
	return scanUser(row)
}

func (s *Store) GetUserByID(id string) (*shared.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT id, email, password_hash, master_key, nvidia_key, created_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) UpdateUserNvidiaKey(userID, nvidiaKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET nvidia_key = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, nvidiaKey, userID)
	return err
}

func (s *Store) SaveProviderKey(userID, provider, apiKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO provider_keys (id, user_id, provider, api_key) VALUES (?, ?, ?, ?)`,
		generateID(), userID, provider, apiKey,
	)
	return err
}

func (s *Store) GetProviderKeys(userID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT provider, api_key FROM provider_keys WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make(map[string]string)
	for rows.Next() {
		var provider, apiKey string
		if err := rows.Scan(&provider, &apiKey); err != nil {
			continue
		}
		keys[provider] = apiKey
	}
	return keys, nil
}

func (s *Store) DeleteProviderKey(userID, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM provider_keys WHERE user_id = ? AND provider = ?`, userID, provider)
	return err
}

func (s *Store) SaveModels(provider string, models []*models.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO models (id, provider, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range models {
		data, _ := json.Marshal(m)
		if _, err := stmt.Exec(m.ID, provider, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadModels(provider string) ([]*models.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT data FROM models WHERE provider = ?`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*models.Model
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		var m models.Model
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			continue
		}
		result = append(result, &m)
	}
	return result, nil
}

func (s *Store) CreateSession(userID, token string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, user_id, token, expires_at) VALUES (?, ?, ?, ?)`,
		generateID(), userID, token, expiresAt,
	)
	return err
}

func (s *Store) GetSession(token string) (*shared.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT id, user_id, token, expires_at FROM sessions WHERE token = ? AND expires_at > CURRENT_TIMESTAMP`, token)
	return scanSession(row)
}

func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) CleanExpiredSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP`)
	return err
}

func scanUser(row *sql.Row) (*shared.User, error) {
	var u shared.User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.MasterKey, &u.NvidiaKey, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func scanSession(row *sql.Row) (*shared.Session, error) {
	var s shared.Session
	err := row.Scan(&s.ID, &s.UserID, &s.Token, &s.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

func generateID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomString(8))
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}