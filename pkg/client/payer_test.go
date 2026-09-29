package client

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestResolveRequestPayer(t *testing.T) {
	payer := uuid.New()

	got, err := resolveRequestPayer(payer.String())
	if err != nil {
		t.Fatalf("resolveRequestPayer() error: %v", err)
	}
	if got != payer {
		t.Fatalf("resolveRequestPayer() = %s, want %s", got, payer)
	}
}

func TestResolveRequestPayerErrors(t *testing.T) {
	for name, payer := range map[string]string{
		"empty": "",
		// an all-zero payer is unset, not payer zero
		"all-zero": uuid.Nil.String(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveRequestPayer(payer); !errors.Is(err, ErrorPayerRequired) {
				t.Fatalf("expected ErrorPayerRequired, got %v", err)
			}
		})
	}

	t.Run("not a uuid", func(t *testing.T) {
		if _, err := resolveRequestPayer("not-a-uuid"); err == nil || errors.Is(err, ErrorPayerRequired) {
			t.Fatalf("expected a parse error for a malformed request payer, got %v", err)
		}
	})
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
