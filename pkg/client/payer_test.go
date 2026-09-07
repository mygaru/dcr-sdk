package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// testJwtToken builds an unsigned JWT-shaped token carrying partner.id, which is
// all payerFromJwtToken reads.
func testJwtToken(t *testing.T, partnerID string) []byte {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"partner": map[string]any{"id": partnerID},
		"scopes":  []string{"cloud:segments:touch"},
	})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	enc := base64.RawURLEncoding
	return []byte(enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) +
		"." + enc.EncodeToString(payload) + ".signature-not-verified")
}

func TestResolveRequestPayer(t *testing.T) {
	fromRequest := uuid.New()
	legacy := uuid.New()

	tests := []struct {
		name         string
		requestPayer string
		legacy       uuid.UUID
		want         uuid.UUID
	}{
		{
			name:         "request payer wins over the legacy token",
			requestPayer: fromRequest.String(),
			legacy:       legacy,
			want:         fromRequest,
		},
		{
			name:   "legacy token is the fallback",
			legacy: legacy,
			want:   legacy,
		},
		{
			name:         "an all-zero request payer is unset, not payer zero",
			requestPayer: uuid.Nil.String(),
			legacy:       legacy,
			want:         legacy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRequestPayer(tt.requestPayer, tt.legacy)
			if err != nil {
				t.Fatalf("resolveRequestPayer() error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveRequestPayer() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResolveRequestPayerErrors(t *testing.T) {
	t.Run("nothing to resolve", func(t *testing.T) {
		_, err := resolveRequestPayer("", uuid.Nil)
		if !errors.Is(err, ErrorPayerRequired) {
			t.Fatalf("expected ErrorPayerRequired, got %v", err)
		}
	})

	t.Run("request payer is not a uuid", func(t *testing.T) {
		// A malformed payer must not silently fall through to the legacy token:
		// acting as the wrong partner is worse than failing the call.
		if _, err := resolveRequestPayer("not-a-uuid", uuid.New()); err == nil {
			t.Fatal("expected a parse error for a malformed request payer")
		}
	})
}

func TestPayerFromJwtToken(t *testing.T) {
	partnerID := uuid.New()

	if got := payerFromJwtToken(testJwtToken(t, partnerID.String())); got != partnerID {
		t.Fatalf("payerFromJwtToken() = %s, want %s", got, partnerID)
	}

	// Unusable tokens yield uuid.Nil rather than an error: a caller that passes
	// the payer explicitly must not be blocked by a stale legacy token.
	unusable := map[string][]byte{
		"empty":            nil,
		"not a jwt":        []byte("just-a-string"),
		"bare uuid":        []byte(uuid.NewString()),
		"bad base64":       []byte("header.!!!not-base64!!!.sig"),
		"no partner claim": testJwtToken(t, ""),
		"partner not uuid": testJwtToken(t, "nope"),
	}
	for name, token := range unusable {
		t.Run(name, func(t *testing.T) {
			if got := payerFromJwtToken(token); got != uuid.Nil {
				t.Fatalf("payerFromJwtToken() = %s, want uuid.Nil", got)
			}
		})
	}
}

func TestResolveRulePayerPrefersTheRule(t *testing.T) {
	rule := uuid.New()
	call := uuid.New() // the request-level payer

	// The rule wins: the payer sits on the rule precisely so that one report can
	// bill several clients, which a call-level override would flatten.
	got, err := resolveRulePayer(rule.String(), call)
	if err != nil {
		t.Fatalf("resolveRulePayer() error: %v", err)
	}
	if got != rule {
		t.Fatalf("resolveRulePayer() = %s, want the rule payer %s", got, rule)
	}

	// A rule that names nobody falls back to the call-level default.
	if got, err = resolveRulePayer("", call); err != nil || got != call {
		t.Fatalf("resolveRulePayer(\"\") = %s, %v; want %s, nil", got, err, call)
	}

	// An all-zero rule payer is "unset", not "payer 000...0".
	if got, err = resolveRulePayer(uuid.Nil.String(), call); err != nil || got != call {
		t.Fatalf("resolveRulePayer(nil uuid) = %s, %v; want %s, nil", got, err, call)
	}
}

func TestResolveRulePayerErrors(t *testing.T) {
	if _, err := resolveRulePayer("", uuid.Nil); !errors.Is(err, ErrorPayerRequired) {
		t.Fatalf("expected ErrorPayerRequired, got %v", err)
	}

	// A malformed rule payer must not fall through to the call default: billing
	// the wrong client is worse than failing the call.
	if _, err := resolveRulePayer("not-a-uuid", uuid.New()); err == nil {
		t.Fatal("expected a parse error for a malformed rule payer")
	}
}
