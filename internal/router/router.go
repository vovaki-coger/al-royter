package router

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/vovaki-coger/al-royter/internal/auth"
	"github.com/vovaki-coger/al-royter/internal/config"
	"github.com/vovaki-coger/al-royter/internal/models"
	"github.com/vovaki-coger/al-royter/internal/providers"
	"github.com/vovaki-coger/al-royter/internal/security"
	"github.com/vovaki-coger/al-royter/internal/shared"
	"github.com/vovaki-coger/al-royter/internal/storage"
)

type Router struct {
	config         *config.Config
	store          *storage.Store
	modelRegistry  *models.Registry
	providerManager *providers.Manager
	authManager    *auth.Manager
	googleOAuth    *auth.GoogleOAuth
	logger         *zap.Logger
	engine         *gin.Engine
	authLimiter    *security.Limiter
	apiLimiter     *security.Limiter
}

func New(
	cfg *config.Config,
	store *storage.Store,
	modelRegistry *models.Registry,
	providerManager *providers.Manager,
	authManager *auth.Manager,
	googleOAuth *auth.GoogleOAuth,
	logger *zap.Logger,
) *Router {
	r := &Router{
		config:           cfg,
		store:            store,
		modelRegistry:    modelRegistry,
		providerManager:  providerManager,
		authManager:      authManager,
		googleOAuth:      googleOAuth,
		logger:           logger.Named("router"),
		authLimiter:      security.NewLimiter(0.5, 10),
		apiLimiter:       security.NewLimiter(cfg.Security.RateLimitRPS, cfg.Security.RateLimitBurst),
	}

	r.engine = gin.New()
	r.engine.Use(gin.Recovery())
	r.engine.Use(security.Headers())
	r.engine.Use(r.logMiddleware())
	r.setupRoutes()
	return r
}

func (r *Router) logMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()
		r.logger.Info("Request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", c.ClientIP()),
		)
	}
}

func (r *Router) setupRoutes() {
	api := r.engine.Group("/v1")
	api.Use(security.BodyLimit(8 << 20))
	{
		api.GET("/health", r.healthCheck)
		api.GET("/models", r.handleModelsList)
		api.POST("/chat/completions",
			r.apiLimiter.MiddlewareByKey(r.apiKeyOf),
			r.authManager.MasterKeyMiddleware(),
			r.chatCompletions)
		api.POST("/chat/completions/stream",
			r.apiLimiter.MiddlewareByKey(r.apiKeyOf),
			r.authManager.MasterKeyMiddleware(),
			r.streamChatCompletions)
	}

	authGroup := r.engine.Group("/auth")
	authGroup.Use(r.authLimiter.Middleware())
	authGroup.Use(security.BodyLimit(64 << 10))
	{
		authGroup.POST("/register", r.register)
		authGroup.POST("/login", r.login)
		authGroup.POST("/refresh", r.authManager.AuthMiddleware(), r.refresh)
		authGroup.GET("/me", r.authManager.AuthMiddleware(), r.me)
		authGroup.GET("/google/enabled", r.googleEnabled)
		authGroup.GET("/google", r.googleOAuth.Login)
		authGroup.GET("/google/callback", r.googleOAuth.Callback)
	}

	userGroup := r.engine.Group("/api")
	userGroup.Use(security.BodyLimit(1 << 20))
	userGroup.Use(r.authManager.AuthMiddleware())
	{
		userGroup.GET("/me", r.me)
		userGroup.GET("/providers", r.listProviders)
		userGroup.POST("/providers/:name/key", r.setProviderKey)
		userGroup.DELETE("/providers/:name/key", r.deleteProviderKey)
		userGroup.GET("/keys", r.listKeys)
		userGroup.POST("/nvidia-key", r.setNvidiaKey)
		userGroup.GET("/openrouter-config", r.getOpenRouterConfig)
		userGroup.GET("/models", r.listUserModels)
		userGroup.POST("/sync", r.syncModels)
		userGroup.POST("/chat/completions",
			r.apiLimiter.MiddlewareByKey(r.apiKeyOf),
			r.chatCompletions)
		userGroup.POST("/chat/completions/stream",
			r.apiLimiter.MiddlewareByKey(r.apiKeyOf),
			r.streamChatCompletions)
	}

	r.engine.GET("/privacy", func(c *gin.Context) { c.File("./web/static/privacy.html") })
	r.engine.GET("/terms", func(c *gin.Context) { c.File("./web/static/terms.html") })

	r.engine.NoRoute(r.spaHandler())
	r.engine.Static("/static", "./web/static")
	r.engine.LoadHTMLGlob("web/templates/*")
}

// apiKeyOf keys the rate limiter by master key, so one client cannot starve others.
func (r *Router) apiKeyOf(c *gin.Context) string {
	if token := r.authManager.ExtractToken(c.Request); token != "" {
		return "key:" + token
	}
	return "ip:" + c.ClientIP()
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.engine.ServeHTTP(w, req)
}

func (r *Router) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"time":     time.Now().Unix(),
		"database": r.store.Dialect(),
	})
}

func (r *Router) handleModelsList(c *gin.Context) {
	r.modelRegistry.HandleModelsList(c.Writer, c.Request)
}

func (r *Router) googleEnabled(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"enabled": r.googleOAuth.Enabled()})
}

func (r *Router) register(c *gin.Context) {
	if !r.config.Security.AllowRegistration {
		c.JSON(http.StatusForbidden, gin.H{"error": "Registration is disabled on this server"})
		return
	}

	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if existing, err := r.store.GetUserByEmail(req.Email); err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}

	hash, _ := r.authManager.HashPassword(req.Password)
	masterKey := r.authManager.GenerateMasterKey()

	userID, err := r.store.CreateUser(req.Email, hash, masterKey, "")
	if err != nil {
		r.logger.Error("Failed to create user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	token, expiresAt, _ := r.authManager.CreateToken(userID)
	_ = r.store.CreateSession(userID, token, expiresAt)

	c.JSON(http.StatusCreated, gin.H{
		"user_id":    userID,
		"master_key": masterKey,
		"token":      token,
		"expires_at": expiresAt.Unix(),
	})
}

func (r *Router) login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := r.store.GetUserByEmail(req.Email)
	if err != nil || user == nil || !r.authManager.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	token, expiresAt, _ := r.authManager.CreateToken(user.ID)
	_ = r.store.CreateSession(user.ID, token, expiresAt)

	c.JSON(http.StatusOK, gin.H{
		"user_id":    user.ID,
		"master_key": user.MasterKey,
		"token":      token,
		"expires_at": expiresAt.Unix(),
	})
}

func (r *Router) refresh(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	token, expiresAt, _ := r.authManager.CreateToken(user.ID)
	_ = r.store.CreateSession(user.ID, token, expiresAt)

	c.JSON(http.StatusOK, gin.H{"token": token, "expires_at": expiresAt.Unix()})
}

func (r *Router) me(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	keys, _ := r.store.GetProviderKeys(user.ID)
	c.JSON(http.StatusOK, gin.H{
		"id":           user.ID,
		"email":        user.Email,
		"master_key":   user.MasterKey,
		"nvidia_key":   user.NvidiaKey,
		"provider_keys": keys,
		"created_at":   user.CreatedAt,
	})
}

func (r *Router) listProviders(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	userKeys := map[string]string{}
	if user != nil {
		userKeys, _ = r.store.GetProviderKeys(user.ID)
		if user.NvidiaKey != "" {
			userKeys["nvidia"] = user.NvidiaKey
		}
	}

	providers := r.providerManager.GetAll()
	result := make([]gin.H, 0, len(providers))
	for _, p := range providers {
		result = append(result, gin.H{
			"name":     p.Name,
			"api_base": p.APIBase,
			"has_key":  p.APIKey != "" || userKeys[p.Name] != "",
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["name"].(string) < result[j]["name"].(string)
	})
	c.JSON(http.StatusOK, gin.H{"providers": result})
}

func (r *Router) setProviderKey(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	providerName := c.Param("name")
	if _, ok := r.providerManager.Get(providerName); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Provider not found"})
		return
	}

	var req struct {
		APIKey string `json:"api_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := r.store.SaveProviderKey(user.ID, providerName, req.APIKey); err != nil {
		r.logger.Error("Failed to save provider key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save key"})
		return
	}

	// The key stays per-user: mutating the shared provider would leak it to
	// every other account that uses the same provider.
	if providerName == "nvidia" {
		_ = r.store.UpdateUserNvidiaKey(user.ID, req.APIKey)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Key saved", "provider": providerName})
}

func (r *Router) deleteProviderKey(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	providerName := c.Param("name")
	if err := r.store.DeleteProviderKey(user.ID, providerName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete key"})
		return
	}

	r.providerManager.SetAPIKey(providerName, "")
	c.JSON(http.StatusOK, gin.H{"message": "Key deleted", "provider": providerName})
}

func (r *Router) listKeys(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	keys, _ := r.store.GetProviderKeys(user.ID)
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

func (r *Router) setNvidiaKey(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		APIKey string `json:"api_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := r.store.UpdateUserNvidiaKey(user.ID, req.APIKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save NVIDIA key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "NVIDIA key saved"})
}

func (r *Router) getOpenRouterConfig(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	baseURL := r.config.OpenRouter.Fields[1].Value
	if strings.Contains(baseURL, "YOUR_DOMAIN") {
		host := c.Request.Host
		if strings.Contains(host, "localhost") || strings.Contains(host, "127.0.0.1") {
			baseURL = "http://" + host + "/v1"
		} else {
			baseURL = "https://" + host + "/v1"
		}
	}

	fields := make([]gin.H, len(r.config.OpenRouter.Fields))
	for i, f := range r.config.OpenRouter.Fields {
		val := f.Value
		if f.Name == "API Key" {
			val = user.MasterKey
		} else if f.Name == "Base URL" && strings.Contains(f.Value, "YOUR_DOMAIN") {
			val = baseURL
		}
		fields[i] = gin.H{
			"name":        f.Name,
			"value":       val,
			"description": f.Description,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"name":        "AL-Router",
		"base_url":    baseURL,
		"api_key":     user.MasterKey,
		"models":      "auto",
		"description": "Unified AI router for NVIDIA Build and other providers",
		"fields":      fields,
		"docs_url":    r.config.OpenRouter.DocsURL,
		"config_url":  r.config.OpenRouter.ConfigURL,
		"instructions": `1. Откройте https://openrouter.ai/settings/keys
2. Нажмите "Add Provider" или "Create API Key"
3. Заполните поля:
   - Name: AL-Router
   - Base URL: ` + baseURL + `
   - API Key: ` + user.MasterKey + `
   - Models: auto (оставьте как есть)
4. Сохраните. Теперь вы можете использовать все модели через OpenRouter!`,
	})
}

func (r *Router) listUserModels(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	keys, _ := r.store.GetProviderKeys(user.ID)
	allModels := r.modelRegistry.GetAll()

	userModels := make([]*shared.Model, 0, len(allModels))
	for _, m := range allModels {
		if _, ok := keys[m.Provider]; ok || m.Provider == "nvidia" {
			userModels = append(userModels, m)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"models": userModels,
		"count":  len(userModels),
	})
}

func (r *Router) syncModels(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	userKeys, _ := r.store.GetProviderKeys(user.ID)
	if user.NvidiaKey != "" {
		userKeys["nvidia"] = user.NvidiaKey
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Minute)
	defer cancel()

	r.modelRegistry.SyncAllWithKeys(ctx, userKeys)
	c.JSON(http.StatusOK, gin.H{"message": "Sync completed"})
}

func (r *Router) chatCompletions(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req providers.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	providerName, modelID := parseModelID(req.Model)
	if providerName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid model format. Use provider/model-id"})
		return
	}

	provider, status, msg := r.providerForUser(providerName, user)
	if provider == nil {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	req.Model = modelID
	resp, err := provider.ChatCompletion(c.Request.Context(), &req)
	if err != nil {
		r.logger.Error("Chat completion failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	resp.Model = req.Model
	c.JSON(http.StatusOK, resp)
}

func (r *Router) streamChatCompletions(c *gin.Context) {
	user := auth.GetUser(c.Request.Context())
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req providers.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	providerName, modelID := parseModelID(req.Model)
	if providerName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid model format. Use provider/model-id"})
		return
	}

	provider, status, msg := r.providerForUser(providerName, user)
	if provider == nil {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	req.Model = modelID
	resp, err := provider.StreamChatCompletion(c.Request.Context(), &req)
	if err != nil {
		r.logger.Error("Stream chat failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			c.Writer.Write(buf[:n])
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			r.logger.Error("Stream read error", zap.Error(err))
			break
		}
	}
}

func parseModelID(model string) (provider, modelID string) {
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// providerForUser returns a provider copy that carries this user's key.
// Mutating the shared provider would leak keys between accounts.
func (r *Router) providerForUser(name string, user *shared.User) (*providers.Provider, int, string) {
	p, ok := r.providerManager.Get(name)
	if !ok {
		return nil, http.StatusNotFound, "Provider not found"
	}

	key := p.APIKey
	if key == "" {
		keys, _ := r.store.GetProviderKeys(user.ID)
		if k := keys[name]; k != "" {
			key = k
		} else if name == "nvidia" && user.NvidiaKey != "" {
			key = user.NvidiaKey
		}
	}
	if key == "" {
		return nil, http.StatusUnauthorized, "No API key configured for provider " + name
	}

	return p.WithKey(key), 0, ""
}

func (r *Router) spaHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") ||
			strings.HasPrefix(c.Request.URL.Path, "/auth") ||
			strings.HasPrefix(c.Request.URL.Path, "/v1") ||
			strings.HasPrefix(c.Request.URL.Path, "/static") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		c.HTML(http.StatusOK, "index.html", gin.H{
			"config": r.config,
		})
	}
}

func (r *Router) Engine() *gin.Engine {
	return r.engine
}