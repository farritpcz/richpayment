package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// IPWhitelist restricts access to a set of allowed IP addresses or CIDR ranges.
type IPWhitelist struct {
	allowed []*net.IPNet
	logger  *slog.Logger
}

// NewIPWhitelist creates a new IPWhitelist middleware.
// cidrs is a list of allowed IP addresses or CIDR notation strings (e.g. "10.0.0.0/8", "192.168.1.1").
func NewIPWhitelist(cidrs []string, logger *slog.Logger) *IPWhitelist {
	var nets []*net.IPNet
	for _, cidr := range cidrs {
		if !strings.Contains(cidr, "/") {
			cidr += "/32"
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			logger.Warn("invalid CIDR in IP whitelist, skipping", "cidr", cidr, "error", err)
			continue
		}
		nets = append(nets, ipNet)
	}
	return &IPWhitelist{
		allowed: nets,
		logger:  logger,
	}
}

// Middleware returns the HTTP middleware function.
func (iw *IPWhitelist) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If no whitelist is configured, allow all.
		if len(iw.allowed) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		clientIP := extractIP(r)
		ip := net.ParseIP(clientIP)
		if ip == nil {
			writeErrorJSON(w, http.StatusForbidden, "INVALID_IP", "unable to determine client IP")
			return
		}

		for _, ipNet := range iw.allowed {
			if ipNet.Contains(ip) {
				next.ServeHTTP(w, r)
				return
			}
		}

		iw.logger.Warn("blocked request from non-whitelisted IP", "ip", clientIP, "path", r.URL.Path)
		writeErrorJSON(w, http.StatusForbidden, "IP_NOT_ALLOWED", "your IP address is not allowed")
	})
}

// extractIP extracts the client IP from the request, checking X-Forwarded-For first.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
