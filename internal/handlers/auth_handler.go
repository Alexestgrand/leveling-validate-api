package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/leveling-unite/validate-api/internal/config"
	"github.com/leveling-unite/validate-api/internal/discord"
	"github.com/leveling-unite/validate-api/internal/middleware"
	redisstore "github.com/leveling-unite/validate-api/internal/redis"
)

const oauthStateTTL = 10 * time.Minute

// AuthHandler manages Discord OAuth2 and session endpoints.
type AuthHandler struct {
	cfg   *config.Config
	oauth *discord.OAuthClient
	redis *redisstore.Client
}

func NewAuthHandler(cfg *config.Config, oauth *discord.OAuthClient, redis *redisstore.Client) *AuthHandler {
	return &AuthHandler{cfg: cfg, oauth: oauth, redis: redis}
}

func (h *AuthHandler) cookieSecure() bool {
	return h.cfg.IsProduction()
}

// SameSite=None is only needed for cross-domain cookies. With the Vercel /api proxy,
// COOKIE_DOMAIN is empty and cookies are first-party on the front domain.
func (h *AuthHandler) cookieCrossOrigin() bool {
	return h.cfg.IsProduction() && h.cfg.CookieDomain != ""
}

func (h *AuthHandler) redirectAuthError(c *gin.Context, code string) {
	target := h.cfg.FrontendURL + "/auth/error?code=" + url.QueryEscape(code)
	log.Printf("oauth: redirect error code=%s path=%s", code, c.Request.URL.Path)
	c.Redirect(http.StatusFound, target)
}

// DiscordRedirect initiates the OAuth2 Authorization Code Flow.
func (h *AuthHandler) DiscordRedirect(c *gin.Context) {
	state, err := randomState()
	if err != nil {
		log.Printf("oauth: random state failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Erreur interne.",
			"code":    "INTERNAL_ERROR",
		})
		return
	}

	// Store state server-side — survives Vercel proxy (unlike a cross-domain cookie).
	if err := h.redis.StoreOAuthState(c.Request.Context(), state, oauthStateTTL); err != nil {
		log.Printf("oauth: store state failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}

	log.Printf(
		"oauth: start authorize client_id=%s redirect_uri=%s state=%s…",
		h.oauth.ClientID(),
		h.oauth.RedirectURI(),
		trimForLog(state, 8),
	)
	c.Redirect(http.StatusFound, h.oauth.AuthorizeURL(state))
}

// DiscordCallback exchanges the authorization code and issues a session JWT.
func (h *AuthHandler) DiscordCallback(c *gin.Context) {
	if errParam := c.Query("error"); errParam != "" {
		log.Printf("oauth: discord denied error=%s desc=%s", errParam, c.Query("error_description"))
		h.redirectAuthError(c, "OAUTH_DENIED")
		return
	}

	state := c.Query("state")
	ok, err := h.redis.ConsumeOAuthState(c.Request.Context(), state)
	if err != nil {
		log.Printf("oauth: consume state redis error: %v", err)
		h.redirectAuthError(c, "OAUTH_FAILED")
		return
	}
	if !ok {
		log.Printf("oauth: invalid or expired state=%s…", trimForLog(state, 8))
		h.redirectAuthError(c, "INVALID_STATE")
		return
	}

	code := c.Query("code")
	if code == "" {
		log.Printf("oauth: callback missing authorization code")
		h.redirectAuthError(c, "OAUTH_FAILED")
		return
	}

	ctx := c.Request.Context()
	log.Printf(
		"oauth: exchanging code redirect_uri=%s client_id=%s code=%s…",
		h.oauth.RedirectURI(),
		h.oauth.ClientID(),
		trimForLog(code, 8),
	)
	accessToken, err := h.oauth.ExchangeCode(ctx, code)
	if err != nil {
		log.Printf("oauth: token exchange failed: %v", err)
		h.redirectAuthError(c, "OAUTH_FAILED")
		return
	}

	user, err := h.oauth.FetchUser(ctx, accessToken)
	if err != nil {
		log.Printf("oauth: fetch user failed: %v", err)
		h.redirectAuthError(c, "OAUTH_FAILED")
		return
	}

	// Single eligibility rule: Discord account age >= MIN_ACCOUNT_AGE_DAYS (snowflake-derived).
	ok, err = discord.IsAccountOldEnough(user.ID, h.cfg.MinAccountAgeDays, time.Now())
	if err != nil {
		log.Printf("oauth: account age check failed user_id=%s: %v", user.ID, err)
		h.redirectAuthError(c, "OAUTH_FAILED")
		return
	}
	if !ok {
		log.Printf("oauth: account too young user_id=%s", user.ID)
		h.redirectAuthError(c, "ACCOUNT_TOO_YOUNG")
		return
	}

	token, err := middleware.IssueJWT(h.cfg.JWTSecret, user.ID, user.DisplayName(), h.cfg.JWTExpiration)
	if err != nil {
		log.Printf("oauth: jwt issue failed user_id=%s: %v", user.ID, err)
		h.redirectAuthError(c, "OAUTH_FAILED")
		return
	}

	middleware.SetSessionCookie(c, token, h.cfg.CookieDomain, h.cookieSecure(), h.cookieCrossOrigin())
	log.Printf(
		"oauth: success user_id=%s username=%s cookie_domain=%q secure=%v cross_origin=%v",
		user.ID,
		user.DisplayName(),
		h.cfg.CookieDomain,
		h.cookieSecure(),
		h.cookieCrossOrigin(),
	)
	c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/auth/success")
}

// Me returns the authenticated user and remaining submission attempts.
func (h *AuthHandler) Me(c *gin.Context) {
	user, ok := middleware.GetAuthUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":       false,
			"message":       "Non authentifié.",
			"code":          "UNAUTHORIZED",
			"authenticated": false,
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

	remaining, err := h.redis.RemainingAttempts(ctx, user.DiscordUserID, h.cfg.MaxAttemptsPerDay)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Service temporairement indisponible.",
			"code":    "REDIS_ERROR",
		})
		return
	}
	if winner {
		remaining = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"authenticated":      true,
		"user_id":            user.DiscordUserID,
		"username":           user.Username,
		"remaining_attempts": remaining,
		"already_won":        winner,
	})
}

// Logout clears the session cookie.
func (h *AuthHandler) Logout(c *gin.Context) {
	middleware.ClearSessionCookie(c, h.cfg.CookieDomain, h.cookieSecure(), h.cookieCrossOrigin())
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Déconnecté."})
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func trimForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
