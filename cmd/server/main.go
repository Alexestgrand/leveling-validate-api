package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/leveling-unite/validate-api/internal/config"
	"github.com/leveling-unite/validate-api/internal/discord"
	"github.com/leveling-unite/validate-api/internal/handlers"
	"github.com/leveling-unite/validate-api/internal/middleware"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
)

func main() {
	// Gin chosen over Fiber: mature ecosystem, widespread production usage,
	// excellent middleware support, and stable API for OAuth/JWT/CORS patterns.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	redisClient, err := redisstore.NewClient(cfg.RedisURL, cfg.RedisTimeout)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisClient.Close()

	oauthClient := discord.NewOAuthClient(cfg.DiscordClientID, cfg.DiscordClientSecret, cfg.DiscordRedirectURI)

	authHandler := handlers.NewAuthHandler(cfg, oauthClient, redisClient)
	validateHandler := handlers.NewValidateHandler(cfg, redisClient)
	healthHandler := handlers.NewHealthHandler(redisClient)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.CORS(cfg.AllowedOrigins))

	router.GET("/health", healthHandler.Health)

	auth := router.Group("/auth")
	{
		auth.GET("/discord", authHandler.DiscordRedirect)
		auth.GET("/discord/callback", authHandler.DiscordCallback)
		auth.GET("/me", middleware.RequireAuth(cfg.JWTSecret), authHandler.Me)
		auth.POST("/logout", middleware.RequireAuth(cfg.JWTSecret), authHandler.Logout)
	}

	// Max 2 KB body on /validate — prevents abuse; phrase is ~15 words max.
	router.POST("/validate",
		func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<10)
			c.Next()
		},
		middleware.RequireAuth(cfg.JWTSecret),
		validateHandler.Validate,
	)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("server listening on :%s (env=%s)", cfg.Port, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	log.Println("server stopped")
}
