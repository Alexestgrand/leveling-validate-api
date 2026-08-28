package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/leveling-unite/validate-api/internal/config"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
)

// EventHandler exposes public event lifecycle status.
type EventHandler struct {
	cfg   *config.Config
	redis *redisstore.Client
}

func NewEventHandler(cfg *config.Config, redis *redisstore.Client) *EventHandler {
	return &EventHandler{cfg: cfg, redis: redis}
}

type eventStatusResponse struct {
	SubmissionOpen bool    `json:"submission_open"`
	FeedbackOpen   bool    `json:"feedback_open"`
	ClosedReason   *string `json:"closed_reason,omitempty"`
	FirstValidAt   *string `json:"first_valid_at,omitempty"`
}

func submissionClosedReason(deadlinePassed, winnerClosed bool) *string {
	switch {
	case winnerClosed:
		reason := "winner"
		return &reason
	case deadlinePassed:
		reason := "deadline"
		return &reason
	default:
		return nil
	}
}

// Status handles GET /event/status — no auth required.
func (h *EventHandler) Status(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()

	deadlinePassed := now.After(h.cfg.SubmissionDeadline)
	winnerClosed, err := h.redis.IsEventSubmissionClosed(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}

	submissionOpen := !deadlinePassed && !winnerClosed
	feedbackOpen := !submissionOpen

	resp := eventStatusResponse{
		SubmissionOpen: submissionOpen,
		FeedbackOpen:   feedbackOpen,
		ClosedReason:   submissionClosedReason(deadlinePassed, winnerClosed),
	}

	if winnerClosed {
		info, err := h.redis.GetEventClosedInfo(ctx)
		if err == nil && info.FirstValid != nil {
			ts := info.FirstValid.UTC().Format(time.RFC3339)
			resp.FirstValidAt = &ts
		}
	}

	c.Header("Cache-Control", "public, max-age=5")
	c.JSON(http.StatusOK, resp)
}
