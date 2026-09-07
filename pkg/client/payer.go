package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrorPayerRequired is returned when a request names no payer and none can be
// derived from the deprecated Configuration.JwtToken.
var ErrorPayerRequired = errors.New("payer uuid is required")

// resolveRequestPayer resolves the caller identity of one request.
//
// The payer on the request is the caller's own identity: it resolves the user
// identifiers, owns the tracking id and pays for the OTP decryption. A request
// that leaves it empty falls back to the partner id of the deprecated
// Configuration.JwtToken, which is what keeps callers that have not migrated
// working.
//
// legacyPayer is pre-resolved from the token at client construction time, so the
// token is parsed once rather than per request.
func resolveRequestPayer(requestPayer string, legacyPayer uuid.UUID) (uuid.UUID, error) {
	if requestPayer != "" {
		parsed, err := uuid.Parse(requestPayer)
		if err != nil {
			return uuid.Nil, fmt.Errorf("payer %q in request is not a uuid: %w", requestPayer, err)
		}
		if parsed != uuid.Nil {
			return parsed, nil
		}
	}

	if legacyPayer != uuid.Nil {
		return legacyPayer, nil
	}

	return uuid.Nil, ErrorPayerRequired
}

// resolveRulePayer resolves the partner billed for one rule.
//
// A rule that already names a payer keeps it - that is the whole point of the
// payer living on the rule: one request may bill several clients. A rule that
// names nobody inherits the request-level payer, so a caller working for a
// single client never has to set it.
func resolveRulePayer(rulePayer string, requestPayer uuid.UUID) (uuid.UUID, error) {
	if rulePayer != "" {
		parsed, err := uuid.Parse(rulePayer)
		if err != nil {
			return uuid.Nil, fmt.Errorf("payer %q in rule is not a uuid: %w", rulePayer, err)
		}
		if parsed != uuid.Nil {
			return parsed, nil
		}
	}

	if requestPayer != uuid.Nil {
		return requestPayer, nil
	}

	return uuid.Nil, ErrorPayerRequired
}

// payerFromJwtToken extracts partner.id from a JWT payload.
//
// Deprecated: this exists only so that callers still configuring
// Configuration.JwtToken keep working while they migrate to naming the payer in
// the request. It does NOT verify the signature - the SDK holds no key and never
// did; the cloud is the only party that can validate a token. Treat the result
// as a convenience default, not as an authenticated identity.
//
// A token that is absent, malformed, or missing partner.id yields uuid.Nil and
// no error: an unusable legacy token must not stop a caller that names the payer
// in the request. The missing payer is reported later, by resolveRequestPayer.
func payerFromJwtToken(token []byte) uuid.UUID {
	if len(token) == 0 {
		return uuid.Nil
	}

	// header.payload.signature - the payload is the only part we need.
	parts := strings.Split(strings.TrimSpace(string(token)), ".")
	if len(parts) < 2 {
		return uuid.Nil
	}

	// JWT uses base64url without padding.
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uuid.Nil
	}

	var claims struct {
		Partner struct {
			ID string `json:"id"`
		} `json:"partner"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return uuid.Nil
	}

	parsed, err := uuid.Parse(claims.Partner.ID)
	if err != nil {
		return uuid.Nil
	}

	return parsed
}
