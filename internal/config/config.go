package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Port                 string
	Env                  string
	RedisURL             string
	DiscordClientID      string
	DiscordClientSecret  string
	DiscordRedirectURI   string
	FrontendURL          string
	JWTSecret            []byte
	SecretPhrase         string
	AllowedOrigins       []string
	CookieDomain         string
	MaxAttemptsPerDay    int
	MinAccountAgeDays    int
	RateLimitWindowHours int
	JWTExpiration        time.Duration
	RedisTimeout         time.Duration
}

// Load reads configuration from environment variables.
// In development, a .env file may be loaded by the caller before invoking Load.
func Load() (*Config, error) {
	maxAttempts, err := strconv.Atoi(getEnv("MAX_ATTEMPTS_PER_DAY", "2"))
	if err != nil {
		return nil, fmt.Errorf("MAX_ATTEMPTS_PER_DAY: %w", err)
	}

	minAge, err := strconv.Atoi(getEnv("MIN_ACCOUNT_AGE_DAYS", "5"))
	if err != nil {
		return nil, fmt.Errorf("MIN_ACCOUNT_AGE_DAYS: %w", err)
	}

	windowHours, err := strconv.Atoi(getEnv("RATE_LIMIT_WINDOW_HOURS", "24"))
	if err != nil {
		return nil, fmt.Errorf("RATE_LIMIT_WINDOW_HOURS: %w", err)
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}

	secretPhrase := os.Getenv("SECRET_PHRASE")
	if secretPhrase == "" {
		return nil, fmt.Errorf("SECRET_PHRASE is required")
	}

	clientID := os.Getenv("DISCORD_CLIENT_ID")
	clientSecret := os.Getenv("DISCORD_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("DISCORD_CLIENT_ID and DISCORD_CLIENT_SECRET are required")
	}

	origins := strings.Split(getEnv("ALLOWED_ORIGINS", ""), ",")
	cleanOrigins := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o != "" {
			cleanOrigins = append(cleanOrigins, o)
		}
	}
	if len(cleanOrigins) == 0 {
		return nil, fmt.Errorf("ALLOWED_ORIGINS must contain at least one origin")
	}

	return &Config{
		Port:                 getEnv("PORT", "8080"),
		Env:                  getEnv("ENV", "development"),
		RedisURL:             getEnv("REDIS_URL", "redis://localhost:6379/0"),
		DiscordClientID:      clientID,
		DiscordClientSecret:  clientSecret,
		DiscordRedirectURI:   getEnv("DISCORD_REDIRECT_URI", "http://localhost:8080/auth/discord/callback"),
		FrontendURL:          getEnv("FRONTEND_URL", "http://localhost:5173"),
		JWTSecret:            []byte(jwtSecret),
		SecretPhrase:         secretPhrase,
		AllowedOrigins:       cleanOrigins,
		CookieDomain:         getEnv("COOKIE_DOMAIN", "localhost"),
		MaxAttemptsPerDay:    maxAttempts,
		MinAccountAgeDays:    minAge,
		RateLimitWindowHours: windowHours,
		JWTExpiration:        24 * time.Hour,
		RedisTimeout:         2 * time.Second,
	}, nil
}

func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
