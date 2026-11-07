// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/config"
)

// A deployment that sets no stripe gets the accent; one that names a stripe
// gets it; and a colour that is not one of the console's is refused, never
// passed to a page as a raw value.
func TestTheConsoleStripeIsOneOfTheConsoles(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(listenersAndDB))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Console.Stripe != config.DefaultConsoleStripe {
		t.Fatalf("an unset stripe is %q", cfg.Console.Stripe)
	}
	for _, stripe := range config.ConsoleStripes {
		cfg, err := config.Parse(strings.NewReader(listenersAndDB + "console: {stripe: " + stripe + "}\n"))
		if err != nil || cfg.Console.Stripe != stripe {
			t.Errorf("stripe %s: %v", stripe, err)
		}
	}
	for _, bad := range []string{"red", "#ff0000", "Accent", "deny"} {
		_, err := config.Parse(strings.NewReader(listenersAndDB + "console: {stripe: \"" + bad + "\"}\n"))
		var fe *config.FieldError
		if !errors.As(err, &fe) || fe.Field != "console.stripe" {
			t.Errorf("stripe %q: %v, want a refusal of console.stripe", bad, err)
		}
	}
}
