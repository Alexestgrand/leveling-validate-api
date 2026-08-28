package handlers

import (
	"context"
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

func (h *ValidateHandler) submissionClosed(ctx context.Context) (bool, string, error) {
	if time.Now().After(h.cfg.SubmissionDeadline) {
		return true, "deadline", nil
	}
	closed, err := h.redis.IsEventSubmissionClosed(ctx)
	if err != nil {
		return false, "", err
	}
	if closed {
		return true, "winner", nil
	}
	return false, "", nil
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

	closed, reason, err := h.submissionClosed(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}
	if closed {
		msg := "La fenêtre de soumission est terminée."
		if reason == "winner" {
			msg = "Un camp a déjà validé la phrase. La soumission est close."
		}
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": msg,
			"code":    "SUBMISSION_CLOSED",
		})
		return
	}

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

	// Best-effort public counters — never block validation on stats failure.
	_ = h.redis.RecordSubmissionStats(ctx, user.DiscordUserID)

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

		if _, err := h.redis.CloseEventSubmissions(ctx, user.DiscordUserID); err != nil {
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
