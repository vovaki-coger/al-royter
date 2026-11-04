package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/vovaki-coger/al-royter/internal/shared"
)

type Store struct {
	db      *sql.DB
	dialect string // "sqlite" | "postgres"
	logger  *zap.Logger
	mu      sync.Mutex
}

type DatabaseConfig struct {
	Type string
	Path string
	URL  string
}

func New(cfg DatabaseConfig) (*Store, error) {
	if cfg.URL != "" {
		u := strings.TrimSpace(cfg.URL)

		if isNeonAPIKey(u) {
			zap.L().Info("DATABASE_URL looks like a Neon API key — resolving connection string automatically")
			resolved, err := resolveNeonURI(u)
			if err != nil {
				zap.L().Error("Failed to resolve Neon connection string, falling back to SQLite", zap.Error(err))
				return newSQLite(cfg.Path)
			}
			u = resolved
			zap.L().Info("Neon connection string resolved")
		}

		if !strings.HasPrefix(u, "postgres://") && !strings.HasPrefix(u, "postgresql://") {
			zap.L().Error("DATABASE_URL is not a Postgres URL — ignoring it and falling back to SQLite. " +
				"Expected: postgresql://user:pass@host/db?sslmode=require, or a Neon API key (napi_...)")
			return newSQLite(cfg.Path)
		}

		pg, err := newPostgres(u)
		if err != nil {
			zap.L().Error("Postgres unavailable, falling back to SQLite (data will NOT persist on Render)", zap.Error(err))
			return newSQLite(cfg.Path)
		}
		return pg, nil
	}
	return newSQLite(cfg.Path)
}

func (s *Store) Dialect() string {
	return s.dialect
}

func newPostgres(url string) (*Store, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}

	s := &Store{db: db, dialect: "postgres", logger: zap.L().Named("storage")}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func newSQLite(path string) (*Store, error) {
	if path == "" {
		path = "./data/al-royter.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	s := &Store{db: db, dialect: "sqlite", logger: zap.L().Named("storage")}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) isPg() bool { return s.dialect == "postgres" }

// q converts ?-placeholders to $1..$n for Postgres.
func (s *Store) q(query string) string {
	if !s.isPg() {
		return query
	}
	var b strings.Builder
	n := 1
	for _, r := range query {
		if r == '?' {
			b.WriteString(fmt.Sprintf("$%d", n))
			n++
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) migrate() error {
	ts := "DATETIME"
	if s.isPg() {
		ts = "TIMESTAMP"
	}

	queries := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE,
			password_hash TEXT,
			master_key TEXT UNIQUE,
			nvidia_key TEXT,
			created_at %s DEFAULT CURRENT_TIMESTAMP,
			updated_at %s DEFAULT CURRENT_TIMESTAMP
		)`, ts, ts),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS provider_keys (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			api_key TEXT NOT NULL,
			created_at %s DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE(user_id, provider)
		)`, ts),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS models (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			data TEXT NOT NULL,
			updated_at %s DEFAULT CURRENT_TIMESTAMP
		)`, ts),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			token TEXT UNIQUE NOT NULL,
			expires_at %s NOT NULL,
			created_at %s DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`, ts, ts),
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
		s.q(`INSERT INTO users (id, email, password_hash, master_key, nvidia_key) VALUES (?, ?, ?, ?, ?)`),
		id, email, passwordHash, masterKey, nvidiaKey,
	)
	return id, err
}

func (s *Store) GetUserByEmail(email string) (*shared.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(s.q(`SELECT id, email, password_hash, master_key, nvidia_key, created_at FROM users WHERE email = ?`), email)
	return scanUser(row)
}

func (s *Store) GetUserByMasterKey(masterKey string) (*shared.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(s.q(`SELECT id, email, password_hash, master_key, nvidia_key, created_at FROM users WHERE master_key = ?`), masterKey)
	return scanUser(row)
}

func (s *Store) GetUserByID(id string) (*shared.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(s.q(`SELECT id, email, password_hash, master_key, nvidia_key, created_at FROM users WHERE id = ?`), id)
	return scanUser(row)
}

func (s *Store) UpdateUserNvidiaKey(userID, nvidiaKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(s.q(`UPDATE users SET nvidia_key = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`), nvidiaKey, userID)
	return err
}

func (s *Store) SaveProviderKey(userID, provider, apiKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var query string
	if s.isPg() {
		query = `INSERT INTO provider_keys (id, user_id, provider, api_key) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, provider) DO UPDATE SET api_key = EXCLUDED.api_key`
	} else {
		query = `INSERT OR REPLACE INTO provider_keys (id, user_id, provider, api_key) VALUES (?, ?, ?, ?)`
	}

	_, err := s.db.Exec(s.q(query), generateID(), userID, provider, apiKey)
	return err
}

func (s *Store) GetProviderKeys(userID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(s.q(`SELECT provider, api_key FROM provider_keys WHERE user_id = ?`), userID)
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
	_, err := s.db.Exec(s.q(`DELETE FROM provider_keys WHERE user_id = ? AND provider = ?`), userID, provider)
	return err
}

func (s *Store) SaveModels(provider string, models []*shared.Model) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Replace the provider's list: models dropped by the availability check
	// must disappear from storage too.
	if _, err := tx.Exec(s.q(`DELETE FROM models WHERE provider = ?`), provider); err != nil {
		return err
	}

	var query string
	if s.isPg() {
		query = `INSERT INTO models (id, provider, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT (id) DO UPDATE SET provider = EXCLUDED.provider, data = EXCLUDED.data, updated_at = CURRENT_TIMESTAMP`
	} else {
		query = `INSERT OR REPLACE INTO models (id, provider, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	}

	stmt, err := tx.Prepare(s.q(query))
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

func (s *Store) LoadModels(provider string) ([]*shared.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(s.q(`SELECT data FROM models WHERE provider = ?`), provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*shared.Model
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		var m shared.Model
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
		s.q(`INSERT INTO sessions (id, user_id, token, expires_at) VALUES (?, ?, ?, ?)`),
		generateID(), userID, token, expiresAt,
	)
	return err
}

func (s *Store) GetSession(token string) (*shared.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(s.q(`SELECT id, user_id, token, expires_at FROM sessions WHERE token = ? AND expires_at > CURRENT_TIMESTAMP`), token)
	return scanSession(row)
}

func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(s.q(`DELETE FROM sessions WHERE token = ?`), token)
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