package handlers

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/leveling-unite/validate-api/internal/config"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
)

const (
	feedbackMinRunes = 3
	feedbackMaxRunes = 500
)

// FeedbackHandler serves anonymous public feedback after the event ends.
type FeedbackHandler struct {
	cfg   *config.Config
	redis *redisstore.Client
}

func NewFeedbackHandler(cfg *config.Config, redis *redisstore.Client) *FeedbackHandler {
	return &FeedbackHandler{cfg: cfg, redis: redis}
}

type feedbackRequest struct {
	Message string `json:"message"`
}

func (h *FeedbackHandler) submissionsOpen(c *gin.Context) (bool, error) {
	ctx := c.Request.Context()
	if time.Now().After(h.cfg.SubmissionDeadline) {
		return false, nil
	}
	closed, err := h.redis.IsEventSubmissionClosed(ctx)
	if err != nil {
		return false, err
	}
	return !closed, nil
}

// List handles GET /feedback — public list, only meaningful once feedback is open.
func (h *FeedbackHandler) List(c *gin.Context) {
	entries, err := h.redis.ListFeedback(c.Request.Context(), 100)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}

	if entries == nil {
		entries = []redisstore.FeedbackEntry{}
	}

	c.Header("Cache-Control", "public, max-age=10")
	c.JSON(http.StatusOK, gin.H{
		"entries": entries,
	})
}

// Create handles POST /feedback — anonymous, rate-limited by IP.
func (h *FeedbackHandler) Create(c *gin.Context) {
	open, err := h.submissionsOpen(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}
	if open {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Les avis seront disponibles une fois l'événement terminé.",
			"code":    "EVENT_NOT_FINISHED",
		})
		return
	}

	var req feedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Requête invalide.",
			"code":    "BAD_REQUEST",
		})
		return
	}

	message := strings.TrimSpace(req.Message)
	runeCount := utf8.RuneCountInString(message)
	if runeCount < feedbackMinRunes || runeCount > feedbackMaxRunes {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Le message doit contenir entre 3 et 500 caractères.",
			"code":    "BAD_REQUEST",
		})
		return
	}

	ip := c.ClientIP()
	limited, err := h.redis.IsFeedbackRateLimited(c.Request.Context(), ip)
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
			"success": false,
			"message": "Limite atteinte. Réessayez dans une heure.",
			"code":    "RATE_LIMITED",
		})
		return
	}

	entry, err := h.redis.RecordFeedback(c.Request.Context(), ip, message)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"entry":   entry,
	})
}
