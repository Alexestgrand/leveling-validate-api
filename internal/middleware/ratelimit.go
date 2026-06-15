package middleware

// Rate limiting is implemented in the validate handler using Redis (see handlers/validate_handler.go).
// Keeping a dedicated package path allows future extraction into middleware if needed.
//
// Design choice: rate limit lives in the handler (not Gin middleware) because:
//   - Attempt counter must increment only on /validate POST, not on every authenticated route
//   - remaining_attempts must reflect post-increment state in the JSON response
//   - Redis is the single source of truth for horizontal scaling (no in-memory counters)
