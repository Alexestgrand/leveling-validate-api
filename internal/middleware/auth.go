package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	CookieName   = "session"
	ContextUser  = "auth_user"
	maxCookieAge = 86400 // 24h in seconds
)

// AuthUser is stored in the Gin context after successful JWT validation.
type AuthUser struct {
	DiscordUserID string
	Username      string
}

type Claims struct {
	DiscordUserID string `json:"discord_user_id"`
	Username      string `json:"username"`
	jwt.RegisteredClaims
}

// IssueJWT creates a signed HS256 token for the authenticated Discord user.
func IssueJWT(secret []byte, userID, username string, expiration time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		DiscordUserID: userID,
		Username:      username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiration)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseJWT validates a JWT string and returns the embedded claims.
func ParseJWT(secret []byte, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// ExtractToken reads the JWT from the httpOnly cookie or Authorization Bearer header.
func ExtractToken(c *gin.Context) string {
	if cookie, err := c.Cookie(CookieName); err == nil && cookie != "" {
		return cookie
	}
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// CookieSameSite returns Lax for same-site dev (localhost) and None for cross-origin prod
// (front Vercel ↔ API sur un autre domaine). None exige Secure=true.
func CookieSameSite(crossOrigin bool) http.SameSite {
	if crossOrigin {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

// SetSessionCookie writes the JWT into an httpOnly session cookie.
func SetSessionCookie(c *gin.Context, token string, domain string, secure, crossOrigin bool) {
	c.SetSameSite(CookieSameSite(crossOrigin))
	c.SetCookie(CookieName, token, maxCookieAge, "/", domain, secure, true)
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(c *gin.Context, domain string, secure, crossOrigin bool) {
	c.SetSameSite(CookieSameSite(crossOrigin))
	c.SetCookie(CookieName, "", -1, "/", domain, secure, true)
}

// RequireAuth validates JWT and injects AuthUser into the Gin context.
func RequireAuth(jwtSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := ExtractToken(c)
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Non authentifié.",
				"code":    "UNAUTHORIZED",
			})
			return
		}

		claims, err := ParseJWT(jwtSecret, tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Non authentifié.",
				"code":    "UNAUTHORIZED",
			})
			return
		}

		c.Set(ContextUser, AuthUser{
			DiscordUserID: claims.DiscordUserID,
			Username:      claims.Username,
		})
		c.Next()
	}
}

// GetAuthUser retrieves the authenticated user from context.
func GetAuthUser(c *gin.Context) (AuthUser, bool) {
	val, ok := c.Get(ContextUser)
	if !ok {
		return AuthUser{}, false
	}
	user, ok := val.(AuthUser)
	return user, ok
}

// CORS configures cross-origin access for the SvelteKit frontend (credentials: true).
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Credentials", "true")
				c.Header("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// SecurityHeaders adds baseline security headers on every response.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		c.Next()
	}
}
