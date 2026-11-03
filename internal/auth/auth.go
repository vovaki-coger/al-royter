package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"go.uber.org/zap"

	"github.com/vovaki-coger/al-royter/internal/config"
	"github.com/vovaki-coger/al-royter/internal/storage"
)

type Manager struct {
	config *config.JWTConfig
	store  *storage.Store
	logger *zap.Logger
}

func NewManager(cfg *config.JWTConfig, store *storage.Store, logger *zap.Logger) *Manager {
	return &Manager{
		config: cfg,
		store:  store,
		logger: logger.Named("auth"),
	}
}

func (m *Manager) HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func (m *Manager) CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func (m *Manager) GenerateMasterKey() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return "al-" + hex.EncodeToString(bytes)
}

func (m *Manager) CreateToken(userID string) (string, time.Time, error) {
	expiresAt := time.Now().Add(time.Duration(m.config.ExpiryHours) * time.Hour)
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     expiresAt.Unix(),
		"iat":     time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(m.config.Secret))
	return tokenString, expiresAt, err
}

func (m *Manager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(m.config.Secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	userID, _ := claims["user_id"].(string)
	exp, _ := claims["exp"].(float64)

	return &Claims{
		UserID: userID,
		Exp:    time.Unix(int64(exp), 0),
	}, nil
}

func (m *Manager) ExtractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return auth
}

func (m *Manager) AuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := m.ExtractToken(r)
			if token == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			claims, err := m.ValidateToken(token)
			if err != nil {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			user, err := m.store.GetUserByID(claims.UserID)
			if err != nil || user == nil {
				http.Error(w, "User not found", http.StatusUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = WithUser(ctx, user)
			ctx = WithClaims(ctx, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (m *Manager) MasterKeyMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := m.ExtractToken(r)
			if token == "" {
				http.Error(w, "API Key required", http.StatusUnauthorized)
				return
			}

			user, err := m.store.GetUserByMasterKey(token)
			if err != nil || user == nil {
				http.Error(w, "Invalid API Key", http.StatusUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = WithUser(ctx, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type Claims struct {
	UserID string
	Exp    time.Time
}

type contextKey string

const (
	userContextKey  contextKey = "user"
	claimsContextKey contextKey = "claims"
)

func WithUser(ctx context.Context, user *storage.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) *storage.User {
	if u, ok := ctx.Value(userContextKey).(*storage.User); ok {
		return u
	}
	return nil
}

func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

func GetClaims(ctx context.Context) *Claims {
	if c, ok := ctx.Value(claimsContextKey).(*Claims); ok {
		return c
	}
	return nil
}