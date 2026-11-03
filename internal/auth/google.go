package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/vovaki-coger/al-royter/internal/config"
	"github.com/vovaki-coger/al-royter/internal/storage"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleUserURL  = "https://www.googleapis.com/oauth2/v2/userinfo"
)

type GoogleOAuth struct {
	cfg         config.GoogleConfig
	store       *storage.Store
	authManager *Manager
	logger      *zap.Logger
}

func NewGoogleOAuth(cfg config.GoogleConfig, store *storage.Store, m *Manager, logger *zap.Logger) *GoogleOAuth {
	return &GoogleOAuth{
		cfg:         cfg,
		store:       store,
		authManager: m,
		logger:      logger.Named("google"),
	}
}

func (g *GoogleOAuth) Enabled() bool {
	return g.cfg.ClientID != "" && g.cfg.ClientSecret != ""
}

func (g *GoogleOAuth) redirectURL(c *gin.Context) string {
	if g.cfg.RedirectURL != "" {
		return g.cfg.RedirectURL
	}
	scheme := "https"
	host := c.Request.Host
	if strings.Contains(host, "localhost") || strings.Contains(host, "127.0.0.1") {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/auth/google/callback", scheme, host)
}

func (g *GoogleOAuth) Login(c *gin.Context) {
	if !g.Enabled() {
		c.Redirect(http.StatusFound, "/?error=google_disabled")
		return
	}

	state := randomState()
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("oauth_state", state, 600, "/", "", c.Request.TLS != nil, true)

	params := url.Values{
		"client_id":     {g.cfg.ClientID},
		"redirect_uri":  {g.redirectURL(c)},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"prompt":        {"select_account"},
		"access_type":   {"online"},
	}
	c.Redirect(http.StatusFound, googleAuthURL+"?"+params.Encode())
}

func (g *GoogleOAuth) Callback(c *gin.Context) {
	if !g.Enabled() {
		c.Redirect(http.StatusFound, "/?error=google_disabled")
		return
	}

	state := c.Query("state")
	cookieState, _ := c.Cookie("oauth_state")
	if state == "" || state != cookieState {
		g.logger.Warn("OAuth state mismatch")
		c.Redirect(http.StatusFound, "/?error=invalid_state")
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", false, true)

	if errParam := c.Query("error"); errParam != "" {
		c.Redirect(http.StatusFound, "/?error="+url.QueryEscape(errParam))
		return
	}

	code := c.Query("code")
	if code == "" {
		c.Redirect(http.StatusFound, "/?error=no_code")
		return
	}

	accessToken, err := g.exchangeCode(code, g.redirectURL(c))
	if err != nil {
		g.logger.Error("Code exchange failed", zap.Error(err))
		c.Redirect(http.StatusFound, "/?error=exchange_failed")
		return
	}

	info, err := g.fetchUser(accessToken)
	if err != nil {
		g.logger.Error("Userinfo failed", zap.Error(err))
		c.Redirect(http.StatusFound, "/?error=userinfo_failed")
		return
	}
	if info.Email == "" {
		c.Redirect(http.StatusFound, "/?error=no_email")
		return
	}

	user, err := g.store.GetUserByEmail(info.Email)
	if err != nil {
		g.logger.Error("DB error", zap.Error(err))
		c.Redirect(http.StatusFound, "/?error=db_error")
		return
	}

	var userID string
	if user == nil {
		masterKey := g.authManager.GenerateMasterKey()
		userID, err = g.store.CreateUser(info.Email, "", masterKey, "")
		if err != nil {
			g.logger.Error("Failed to create google user", zap.Error(err))
			c.Redirect(http.StatusFound, "/?error=create_failed")
			return
		}
		g.logger.Info("Google user created", zap.String("email", info.Email), zap.String("user_id", userID))
	} else {
		userID = user.ID
	}

	token, expiresAt, err := g.authManager.CreateToken(userID)
	if err != nil {
		c.Redirect(http.StatusFound, "/?error=token_failed")
		return
	}
	_ = g.store.CreateSession(userID, token, expiresAt)

	c.Redirect(http.StatusFound, "/?token="+url.QueryEscape(token))
}

type googleUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

func (g *GoogleOAuth) exchangeCode(code, redirectURL string) (string, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {g.cfg.ClientID},
		"client_secret": {g.cfg.ClientSecret},
		"redirect_uri":  {redirectURL},
		"grant_type":    {"authorization_code"},
	}
	resp, err := http.Post(googleTokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("token exchange error: %s %s", result.Error, result.ErrorDesc)
	}
	return result.AccessToken, nil
}

func (g *GoogleOAuth) fetchUser(accessToken string) (*googleUser, error) {
	req, _ := http.NewRequest("GET", googleUserURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var u googleUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}

func randomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}