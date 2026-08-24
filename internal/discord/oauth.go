package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorizeURL = "https://discord.com/api/oauth2/authorize"
	tokenURL     = "https://discord.com/api/oauth2/token"
	userMeURL    = "https://discord.com/api/users/@me"

	// Discord/Cloudflare often rate-limits shared cloud egress IPs (Render, CF Workers…).
	maxDiscordRetries = 4
	retryBaseDelay    = 1500 * time.Millisecond

	// Discord /users/@me payloads often exceed 512B (avatar, banner, decorations…).
	maxDiscordBodyBytes = 64 << 10 // 64 KiB
)

func readDiscordBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxDiscordBodyBytes))
}

// User represents the subset of Discord user fields needed by this API.
type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	GlobalName    string `json:"global_name"`
}

// DisplayName returns the best available display name for JWT claims.
func (u User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	if u.Discriminator != "" && u.Discriminator != "0" {
		return u.Username + "#" + u.Discriminator
	}
	return u.Username
}

// OAuthClient handles Discord OAuth2 Authorization Code Flow.
type OAuthClient struct {
	clientID     string
	clientSecret string
	redirectURI  string
	httpClient   *http.Client
}

func NewOAuthClient(clientID, clientSecret, redirectURI string) *OAuthClient {
	return &OAuthClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// ClientID returns the Discord application client id (safe to log).
func (c *OAuthClient) ClientID() string { return c.clientID }

// RedirectURI returns the configured OAuth redirect URI (safe to log).
func (c *OAuthClient) RedirectURI() string { return c.redirectURI }

// AuthorizeURL builds the Discord OAuth2 authorization redirect URL (scope: identify).
func (c *OAuthClient) AuthorizeURL(state string) string {
	params := url.Values{}
	params.Set("client_id", c.clientID)
	params.Set("redirect_uri", c.redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", "identify")
	params.Set("state", state)
	return authorizeURL + "?" + params.Encode()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// RateLimitedError means Discord/Cloudflare rejected the request (429 / CF 1015).
type RateLimitedError struct {
	Status int
	Body   string
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("discord rate limited: status=%d body=%s", e.Status, e.Body)
}

func isRateLimited(status int, body string) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	// Cloudflare HTML/text body often includes "error code: 1015".
	return strings.Contains(body, "1015")
}

func sleepBackoff(ctx context.Context, attempt int, retryAfter time.Duration) error {
	delay := retryAfter
	if delay <= 0 {
		delay = retryBaseDelay * time.Duration(1<<attempt)
	}
	if delay > 12*time.Second {
		delay = 12 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(resp *http.Response) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := time.ParseDuration(ra + "s"); err == nil {
			return secs
		}
	}
	return 0
}

// ExchangeCode trades an authorization code for a Discord access token.
func (c *OAuthClient) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirectURI)
	encoded := form.Encode()

	var lastErr error
	for attempt := 0; attempt < maxDiscordRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(encoded))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("token exchange request failed: %w", err)
			if attempt+1 >= maxDiscordRetries {
				break
			}
			log.Printf("oauth: token exchange network error, retry attempt=%d/%d: %v", attempt+2, maxDiscordRetries, err)
			if err := sleepBackoff(ctx, attempt, 0); err != nil {
				return "", err
			}
			continue
		}

		body, err := readDiscordBody(resp)
		if err != nil {
			lastErr = fmt.Errorf("read token response: %w", err)
			if attempt+1 >= maxDiscordRetries {
				break
			}
			if err := sleepBackoff(ctx, attempt, 0); err != nil {
				return "", err
			}
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var tr tokenResponse
			if err := json.Unmarshal(body, &tr); err != nil {
				return "", fmt.Errorf("decode token response (len=%d): %w", len(body), err)
			}
			if tr.AccessToken == "" {
				return "", fmt.Errorf("empty access token in response")
			}
			return tr.AccessToken, nil
		}

		if isRateLimited(resp.StatusCode, string(body)) {
			lastErr = &RateLimitedError{Status: resp.StatusCode, Body: string(body)}
			retryAfter := parseRetryAfter(resp)
			log.Printf(
				"oauth: discord/cloudflare rate limit on token exchange status=%d attempt=%d/%d retry_after=%s body=%s",
				resp.StatusCode,
				attempt+1,
				maxDiscordRetries,
				retryAfter,
				string(body),
			)
			if attempt+1 >= maxDiscordRetries {
				return "", lastErr
			}
			if err := sleepBackoff(ctx, attempt, retryAfter); err != nil {
				return "", err
			}
			continue
		}

		return "", fmt.Errorf("token exchange failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("token exchange failed after retries")
}

// FetchUser retrieves the authenticated Discord user profile.
func (c *OAuthClient) FetchUser(ctx context.Context, accessToken string) (*User, error) {
	var lastErr error
	for attempt := 0; attempt < maxDiscordRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, userMeURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("fetch user request failed: %w", err)
			if attempt+1 >= maxDiscordRetries {
				break
			}
			log.Printf("oauth: fetch user network error, retry attempt=%d/%d: %v", attempt+2, maxDiscordRetries, err)
			if err := sleepBackoff(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}

		body, err := readDiscordBody(resp)
		if err != nil {
			lastErr = fmt.Errorf("read user response: %w", err)
			if attempt+1 >= maxDiscordRetries {
				break
			}
			if err := sleepBackoff(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var user User
			if err := json.Unmarshal(body, &user); err != nil {
				log.Printf(
					"oauth: decode user failed status=%d body_len=%d preview=%q err=%v",
					resp.StatusCode,
					len(body),
					trimForLog(string(body), 120),
					err,
				)
				return nil, fmt.Errorf("decode user response (len=%d): %w", len(body), err)
			}
			if user.ID == "" {
				return nil, fmt.Errorf("discord user id missing in response")
			}
			log.Printf("oauth: fetch user ok id=%s username=%s body_len=%d", user.ID, user.Username, len(body))
			return &user, nil
		}

		if isRateLimited(resp.StatusCode, string(body)) {
			lastErr = &RateLimitedError{Status: resp.StatusCode, Body: string(body)}
			log.Printf(
				"oauth: discord/cloudflare rate limit on fetch user status=%d attempt=%d/%d body=%s",
				resp.StatusCode,
				attempt+1,
				maxDiscordRetries,
				string(body),
			)
			if attempt+1 >= maxDiscordRetries {
				return nil, lastErr
			}
			if err := sleepBackoff(ctx, attempt, parseRetryAfter(resp)); err != nil {
				return nil, err
			}
			continue
		}

		return nil, fmt.Errorf("fetch user failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("fetch user failed after retries")
}

func trimForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
