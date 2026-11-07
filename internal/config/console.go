// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"slices"
	"strings"
)

// ConsoleConfig is the operator console's (0049, PLAN M20): what the console
// shows that only the deployment's operator can say.
type ConsoleConfig struct {
	// Stripe is the deployment's colour in the console: a bar along the top
	// of every page and the chip behind the deployment's name (ui/DESIGN.md
	// §5), so that two deployments open side by side cannot be mistaken
	// for each other, in a screenshot as much as on screen. It is
	// configuration and never inference — nothing guesses "production" from
	// a hostname. Empty is the console's accent.
	Stripe string `yaml:"stripe"`
}

// ConsoleStripes are the stripes a deployment may choose, each a pair of
// tokens in ui/src/styles/tokens.css. None is a status colour: a status hue
// means a status, and a deployment's colour is not one.
var ConsoleStripes = []string{"accent", "magenta", "teal", "orange", "slate"}

// DefaultConsoleStripe is the stripe of a deployment that sets none.
const DefaultConsoleStripe = "accent"

func (c *Config) applyConsoleDefaults() {
	if c.Console.Stripe == "" {
		c.Console.Stripe = DefaultConsoleStripe
	}
}

func (c ConsoleConfig) validate() error {
	if c.Stripe != "" && !slices.Contains(ConsoleStripes, c.Stripe) {
		return &FieldError{Field: "console.stripe", Msg: "must be one of " + strings.Join(ConsoleStripes, ", ")}
	}
	return nil
}
