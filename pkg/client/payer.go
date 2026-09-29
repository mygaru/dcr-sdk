package client

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrorPayerRequired is returned when a request names no payer.
var ErrorPayerRequired = errors.New("payer uuid is required")

// resolveRequestPayer resolves the caller identity of one request.
//
// The payer on the request is the caller's own identity: it resolves the user
// identifiers, owns the tracking id and pays for the OTP decryption. It is
// required.
func resolveRequestPayer(requestPayer string) (uuid.UUID, error) {
	if requestPayer != "" {
		parsed, err := uuid.Parse(requestPayer)
		if err != nil {
			return uuid.Nil, fmt.Errorf("payer %q in request is not a uuid: %w", requestPayer, err)
		}
		if parsed != uuid.Nil {
			return parsed, nil
		}
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
