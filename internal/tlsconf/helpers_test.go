// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package tlsconf_test

import (
	"bytes"
	"io"
	"log"
	"log/slog"
)

func discardLog() *log.Logger { return log.New(io.Discard, "", 0) }

// fmtAttrs renders log attributes as a text handler would, for matching.
func fmtAttrs(attrs []any) string {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("posture", attrs...)
	return buf.String()
}
