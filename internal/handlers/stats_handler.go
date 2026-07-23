package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
)

// StatsHandler exposes public aggregate submission counters.
type StatsHandler struct {
	redis *redisstore.Client
}

func NewStatsHandler(redis *redisstore.Client) *StatsHandler {
	return &StatsHandler{redis: redis}
}

// Stats handles GET /stats — no auth required.
func (h *StatsHandler) Stats(c *gin.Context) {
	stats, err := h.redis.GetSubmissionStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}

	c.Header("Cache-Control", "public, max-age=2")
	c.JSON(http.StatusOK, gin.H{
		"total_attempts": stats.TotalAttempts,
		"unique_testers": stats.UniqueTesters,
	})
}
