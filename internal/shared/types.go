package shared

import "time"

type User struct {
	ID           string
	Email        string
	PasswordHash string
	MasterKey    string
	NvidiaKey    string
	CreatedAt    time.Time
}

type Session struct {
	ID        string
	UserID    string
	Token     string
	ExpiresAt time.Time
}