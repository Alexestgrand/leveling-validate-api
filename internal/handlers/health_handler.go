package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
)

// HealthHandler exposes liveness and dependency checks.
type HealthHandler struct {
	redis *redisstore.Client
}

func NewHealthHandler(redis *redisstore.Client) *HealthHandler {
	return &HealthHandler{redis: redis}
}

func (h *HealthHandler) Health(c *gin.Context) {
	status := "ok"
	redisStatus := "connected"

	if err := h.redis.Ping(c.Request.Context()); err != nil {
		redisStatus = "disconnected"
		status = "degraded"
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": status,
			"redis":  redisStatus,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": status,
		"redis":  redisStatus,
	})
}
