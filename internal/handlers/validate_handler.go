package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/leveling-unite/validate-api/internal/config"
	"github.com/leveling-unite/validate-api/internal/middleware"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
	"github.com/leveling-unite/validate-api/internal/validate"
)

// ValidateHandler processes phrase submission requests.
type ValidateHandler struct {
	cfg              *config.Config
	redis            *redisstore.Client
	normalizedSecret string // pre-normalized at startup; never logged
}

func NewValidateHandler(cfg *config.Config, redis *redisstore.Client) *ValidateHandler {
	return &ValidateHandler{
		cfg:              cfg,
		redis:            redis,
		normalizedSecret: validate.NormalizePhrase(cfg.SecretPhrase),
	}
}

type validateRequest struct {
	Phrase string `json:"phrase"`
}

// Validate handles POST /validate — the core phrase submission endpoint.
func (h *ValidateHandler) Validate(c *gin.Context) {
	user, ok := middleware.GetAuthUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Non authentifié.",
			"code":    "UNAUTHORIZED",
		})
		return
	}

	ctx := c.Request.Context()

	winner, err := h.redis.IsWinner(ctx, user.DiscordUserID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}
	if winner {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "Phrase déjà validée pour ce compte.",
			"code":    "ALREADY_WON",
		})
		return
	}

	var req validateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Requête invalide.",
			"code":    "BAD_REQUEST",
		})
		return
	}

	if !validate.ValidateSubmission(req.Phrase) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Requête invalide.",
			"code":    "BAD_REQUEST",
		})
		return
	}

	limited, err := h.redis.IsRateLimited(ctx, user.DiscordUserID, h.cfg.MaxAttemptsPerDay)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}
	if limited {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success":            false,
			"message":            "Limite atteinte. Réessayez dans 24h.",
			"remaining_attempts": 0,
			"code":               "RATE_LIMITED",
		})
		return
	}

	// Each valid submission consumes 1 attempt, incremented before phrase comparison.
	window := time.Duration(h.cfg.RateLimitWindowHours) * time.Hour
	count, err := h.redis.IncrementAttempt(ctx, user.DiscordUserID, window)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}

	remaining := h.cfg.MaxAttemptsPerDay - count
	if remaining < 0 {
		remaining = 0
	}

	// NEVER log req.Phrase or the secret phrase.
	normalized := validate.NormalizePhrase(req.Phrase)
	match := validate.PhrasesMatch(normalized, h.normalizedSecret)

	if match {
		if err := h.redis.MarkWinner(ctx, user.DiscordUserID); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "Service temporairement indisponible.",
				"code":    "REDIS_ERROR",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success":            true,
			"message":            "Félicitations ! Phrase validée.",
			"remaining_attempts": remaining,
			"code":               "VALID",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            false,
		"message":            "Phrase incorrecte.",
		"remaining_attempts": remaining,
		"code":               "INVALID",
	})
}
