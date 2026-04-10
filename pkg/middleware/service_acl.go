// This file implements service-level Access Control Lists (ACL) for the
// RichPayment microservices architecture. It restricts which services are
// allowed to call which endpoints, providing defense-in-depth beyond the
// HMAC authentication layer.
//
// # Why ACL Is Needed (Defense-in-Depth)
//
// The InternalAuth middleware (internal_auth.go) verifies that a caller
// possesses the shared secret — but ALL services share the SAME secret.
// If an attacker compromises telegram-service (which has the secret), they
// could call wallet-service to credit unlimited funds.
//
// The ServiceACL middleware adds a second layer: even with valid authentication,
// telegram-service is NOT allowed to call /wallet/credit. Only order-service
// and withdrawal-service have that permission.
//
// # ACL Design
//
// The ACL is defined as a map of path prefixes to lists of allowed services.
// When a request arrives (already authenticated by InternalAuth), the ACL
// middleware checks whether the caller's service name (from X-Internal-Service)
// is in the allowed list for the requested path.
//
// If no ACL rule matches the requested path, the request is ALLOWED by default.
// This means you only need to define rules for sensitive endpoints. Health
// checks and other non-sensitive endpoints pass through without ACL rules.
//
// # Usage
//
// The ACL middleware MUST be applied AFTER the InternalAuth middleware, because
// it reads the X-Internal-Service header which InternalAuth has already
// validated.
//
//	internalAuth := middleware.NewInternalAuth(logger)
//	acl := middleware.NewServiceACL(logger, middleware.DefaultWalletACL())
//	handler := internalAuth.Middleware(acl.Middleware(mux))
package middleware

import (
	"log/slog"
	"net/http"
	"strings"
)

// ACLRule defines an access control rule for a specific path prefix.
// It specifies which services are allowed to call endpoints matching
// the prefix.
type ACLRule struct {
	// PathPrefix is the URL path prefix that this rule applies to.
	// A request path must start with this prefix to match the rule.
	// Example: "/wallet/credit" matches requests to "/wallet/credit"
	// and "/wallet/credit/something".
	PathPrefix string

	// AllowedServices is the set of service names that are permitted to
	// call endpoints matching the PathPrefix. Service names must match
	// the X-Internal-Service header value exactly.
	// Example: []string{"order-service", "withdrawal-service"}
	AllowedServices []string
}

// ServiceACL is an HTTP middleware that enforces service-level access control
// on internal endpoints. It checks whether the calling service (identified by
// the X-Internal-Service header) is authorized to access the requested path.
//
// This middleware provides defense-in-depth beyond HMAC authentication:
// even if a service has valid authentication credentials, it can only access
// endpoints it has been explicitly granted permission to call.
type ServiceACL struct {
	// logger is the structured logger for recording ACL decisions.
	// Denied requests are logged at Warn level for security auditing.
	// Allowed requests are logged at Debug level for troubleshooting.
	logger *slog.Logger

	// rules is the ordered list of ACL rules. Rules are evaluated in order;
	// the first matching rule (by path prefix) determines whether the request
	// is allowed or denied. If no rule matches, the request is allowed
	// (open by default for non-sensitive endpoints).
	rules []ACLRule
}

// NewServiceACL creates a new ServiceACL middleware with the given rules.
//
// Parameters:
//   - logger: structured logger for recording ACL decisions and denials.
//   - rules: the list of ACL rules defining which services can access which
//     path prefixes. Rules are evaluated in order; use the most specific
//     path prefixes first for correct matching.
//
// Returns a ready-to-use *ServiceACL instance.
//
// Example:
//
//	acl := middleware.NewServiceACL(logger, []middleware.ACLRule{
//	    {PathPrefix: "/wallet/credit", AllowedServices: []string{"order-service"}},
//	    {PathPrefix: "/wallet/debit",  AllowedServices: []string{"order-service", "withdrawal-service"}},
//	})
func NewServiceACL(logger *slog.Logger, rules []ACLRule) *ServiceACL {
	return &ServiceACL{
		logger: logger,
		rules:  rules,
	}
}

// Middleware returns an http.Handler middleware function that enforces the
// ACL rules on incoming requests. It must be placed AFTER the InternalAuth
// middleware in the chain, because it relies on the X-Internal-Service header
// having been validated.
//
// Request flow:
//  1. Read X-Internal-Service header (already validated by InternalAuth).
//  2. Find the first ACL rule whose PathPrefix matches the request path.
//  3. If a rule matches, check if the caller is in the AllowedServices list.
//  4. If the caller is NOT allowed, return HTTP 403 Forbidden.
//  5. If no rule matches, allow the request (open by default).
//
// On denial:
//   - Returns HTTP 403 Forbidden with a JSON error body.
//   - Logs the denial at Warn level with: caller service, path, and the
//     list of services that ARE allowed.
func (acl *ServiceACL) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the caller's service name from the header. This header has
		// already been validated by the InternalAuth middleware, so it is
		// guaranteed to be present and authentic at this point.
		callerService := r.Header.Get("X-Internal-Service")

		// Iterate through the ACL rules in order to find the first rule
		// whose path prefix matches the incoming request path.
		for _, rule := range acl.rules {
			// Check if the request path starts with the rule's prefix.
			// strings.HasPrefix is used for prefix matching so that a rule
			// for "/wallet/credit" also covers "/wallet/credit/batch" etc.
			if strings.HasPrefix(r.URL.Path, rule.PathPrefix) {
				// A matching rule was found. Check if the caller is in the
				// allowed services list for this path prefix.
				if !acl.isServiceAllowed(callerService, rule.AllowedServices) {
					// DENIED: The caller's service is not in the allowed list.
					// Log the denial with full context for security auditing.
					acl.logger.Warn("service ACL denied",
						"caller_service", callerService,
						"path", r.URL.Path,
						"matched_rule", rule.PathPrefix,
						"allowed_services", rule.AllowedServices,
						"remote_addr", r.RemoteAddr,
					)

					// Return HTTP 403 Forbidden with a descriptive error message.
					writeInternalAuthError(w, http.StatusForbidden, "SERVICE_NOT_AUTHORIZED",
						callerService+" is not authorized to call "+r.URL.Path)
					return
				}

				// ALLOWED: The caller is in the allowed list for this rule.
				// Log at Debug level and proceed to the next handler.
				acl.logger.Debug("service ACL allowed",
					"caller_service", callerService,
					"path", r.URL.Path,
					"matched_rule", rule.PathPrefix,
				)

				// First matching rule wins — stop checking further rules.
				break
			}
		}

		// Either no rule matched (open by default) or the caller was allowed.
		// Forward the request to the next handler in the chain.
		next.ServeHTTP(w, r)
	})
}

// isServiceAllowed checks whether a given service name appears in the list
// of allowed services. The comparison is case-sensitive and requires an
// exact match.
//
// Parameters:
//   - serviceName: the name of the calling service (from X-Internal-Service).
//   - allowedServices: the list of service names permitted by the ACL rule.
//
// Returns true if serviceName is found in allowedServices, false otherwise.
func (acl *ServiceACL) isServiceAllowed(serviceName string, allowedServices []string) bool {
	for _, allowed := range allowedServices {
		if serviceName == allowed {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------
// Predefined ACL configurations for each RichPayment service.
// These functions return ACL rules tailored to specific services, encoding
// the business logic of which services are permitted to call which endpoints.
// -----------------------------------------------------------------------

// DefaultWalletACL returns the ACL rules for the wallet-service.
//
// The wallet-service manages merchant balances and is the most security-
// sensitive service in the system. Unauthorized credits could cause direct
// financial loss.
//
// Rules:
//   - /wallet/credit: ONLY order-service can credit wallets (after successful
//     deposit confirmation). No other service should be able to add funds.
//   - /wallet/debit: ONLY order-service and withdrawal-service can debit wallets.
//     Order-service debits for refunds; withdrawal-service debits for payouts.
//   - /wallet/freeze and /wallet/unfreeze: ONLY order-service and withdrawal-service
//     can freeze/unfreeze wallet balances during transaction processing.
//   - /wallet/balance: order-service, withdrawal-service, and gateway-api can
//     check balances (for display and validation).
//   - /health: no ACL rule — accessible to all authenticated services.
//
// IMPORTANT: telegram-service is explicitly NOT in any of these lists.
// Even if telegram-service is compromised and has the shared HMAC secret,
// it CANNOT credit, debit, or freeze wallets.
func DefaultWalletACL() []ACLRule {
	return []ACLRule{
		{
			// Credit operations: only order-service can add funds to wallets.
			// This is the most critical rule — unauthorized credits = financial loss.
			PathPrefix:      "/wallet/credit",
			AllowedServices: []string{"order-service"},
		},
		{
			// Debit operations: order-service (refunds) and withdrawal-service (payouts).
			PathPrefix:      "/wallet/debit",
			AllowedServices: []string{"order-service", "withdrawal-service"},
		},
		{
			// Freeze operations: lock funds during transaction processing.
			PathPrefix:      "/wallet/freeze",
			AllowedServices: []string{"order-service", "withdrawal-service"},
		},
		{
			// Unfreeze operations: release locked funds after processing.
			PathPrefix:      "/wallet/unfreeze",
			AllowedServices: []string{"order-service", "withdrawal-service"},
		},
		{
			// Balance queries: readable by services that need balance info.
			PathPrefix:      "/wallet/balance",
			AllowedServices: []string{"order-service", "withdrawal-service", "gateway-api"},
		},
	}
}

// DefaultCommissionACL returns the ACL rules for the commission-service.
//
// The commission-service calculates and records fee splits. Only order-service
// and withdrawal-service need to trigger commission calculations.
//
// Rules:
//   - /internal/commission/calculate: ONLY order-service and withdrawal-service.
//   - /internal/commission/record: ONLY order-service and withdrawal-service.
func DefaultCommissionACL() []ACLRule {
	return []ACLRule{
		{
			// Commission calculation: only triggered by transaction-processing services.
			PathPrefix:      "/internal/commission/calculate",
			AllowedServices: []string{"order-service", "withdrawal-service"},
		},
		{
			// Commission recording: only triggered by transaction-processing services.
			PathPrefix:      "/internal/commission/record",
			AllowedServices: []string{"order-service", "withdrawal-service"},
		},
	}
}

// DefaultNotificationACL returns the ACL rules for the notification-service.
//
// The notification-service sends webhooks and alerts. Multiple services may
// need to trigger notifications, but not all services should have access.
//
// Rules:
//   - /internal/webhook/send: order-service, withdrawal-service, and
//     scheduler-service (for scheduled notifications).
//   - /internal/alert/send: any authenticated service can send alerts
//     (no specific ACL rule — open by default).
func DefaultNotificationACL() []ACLRule {
	return []ACLRule{
		{
			// Webhook delivery: only transaction services and the scheduler.
			PathPrefix: "/internal/webhook/send",
			AllowedServices: []string{
				"order-service",
				"withdrawal-service",
				"scheduler-service",
			},
		},
	}
}

// DefaultOrderACL returns the ACL rules for the order-service.
//
// The order-service manages deposit order lifecycle. External creation comes
// through the gateway, and internal callbacks come from bank-service and
// parser-service.
//
// Rules:
//   - /internal/order/callback: ONLY bank-service and parser-service can
//     send payment confirmation callbacks.
func DefaultOrderACL() []ACLRule {
	return []ACLRule{
		{
			// Payment callbacks: only bank-service and parser-service can confirm payments.
			PathPrefix:      "/internal/order/callback",
			AllowedServices: []string{"bank-service", "parser-service"},
		},
	}
}
