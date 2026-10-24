// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"strings"
)

// Bounded lists (M26). Every north-bound list is read here one page at a time:
// at most Limit rows after a keyset position, in the list's one total order,
// in ONE statement that reads a row past the page to learn whether the list
// continues. A reader names its order and its predicate and nothing else; the
// rules below are the only paging rules in the package.

// DefaultPageLimit and MaxPageLimit are M26's: the limit a list takes when
// its caller names none, and the most one page may hold.
const (
	DefaultPageLimit = 500
	MaxPageLimit     = 10000
)

// Page asks a reader for one page of a list (M26).
type Page struct {
	// Limit is how many rows the page may hold, 1 to MaxPageLimit. The
	// handler has refused anything else; a reader handed it answers
	// KindInvalid and never clamps.
	Limit int
	// After is the sort key of the last row the caller was handed, one value
	// per component of the list's order, each typed as its column is
	// (string, int64, time.Time, bool). Nil starts the list.
	After []any
}

// Paged is one page of a list, and whether the list continues past it.
type Paged[T any] struct {
	Rows []T
	// More reports that at least one row follows the last of Rows.
	More bool
}

// order is a list's one stable total order (M26.4): the columns of its sort
// key, the last of them unique within the tenant, all read in one direction
// so that a row comparison continues it. An index serves it.
type order struct {
	columns []string
	desc    bool
}

// checkPage refuses a page this order cannot read: a limit outside 1 to
// MaxPageLimit, or a position with neither no components nor one per column
// of the key. A reader never clamps (M26.1).
func checkPage(op string, o order, p Page) error {
	if p.Limit < 1 || p.Limit > MaxPageLimit {
		return invalid(op, fmt.Sprintf("page limit %d is outside 1 to %d", p.Limit, MaxPageLimit))
	}
	if len(p.After) != 0 && len(p.After) != len(o.columns) {
		return invalid(op, fmt.Sprintf("page position has %d components, want %d", len(p.After), len(o.columns)))
	}
	return nil
}

// after renders the keyset predicate that continues the list after p.After,
// as a row comparison in the list's direction — `(c1, c2) > ($n, $m)`, or `<`
// for a list read newest first — appending its arguments to args. It is ""
// on the first page. The row compares in the column's own collation, the one
// its ORDER BY and its index use, so the position and the order agree.
func (o order) after(p Page, args []any) (string, []any) {
	if len(p.After) == 0 {
		return "", args
	}
	params := make([]string, len(p.After))
	for i, v := range p.After {
		args = append(args, v)
		params[i] = fmt.Sprintf("$%d", len(args))
	}
	op := ">"
	if o.desc {
		op = "<"
	}
	return "(" + strings.Join(o.columns, ", ") + ") " + op + " (" + strings.Join(params, ", ") + ")", args
}

// clause renders everything a page's statement takes after its own WHERE
// predicate: the keyset predicate, the order, and a limit of one row past the
// page, so scanPage can tell a list that continues from one that ends here.
func (o order) clause(p Page, args []any) (string, []any) {
	var b strings.Builder
	pred, args := o.after(p, args)
	if pred != "" {
		b.WriteString(" AND " + pred)
	}
	b.WriteString(" ORDER BY ")
	for i, c := range o.columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c)
		if o.desc {
			b.WriteString(" DESC")
		}
	}
	args = append(args, p.Limit+1)
	fmt.Fprintf(&b, " LIMIT $%d", len(args))
	return b.String(), args
}

// queryPage runs one page's statement — already ordered and limited to
// p.Limit+1 by clause — and scans it. A failure at any row is an error and
// never a short page (M26.6, M11): what was read before it is discarded.
func queryPage[T any](ctx context.Context, db querier, op string, p Page, sql string, args []any,
	scan func(string, rowScanner) (T, error),
) (Paged[T], error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return Paged[T]{}, wrap(op, err)
	}
	defer rows.Close()

	var out Paged[T]
	for rows.Next() {
		if len(out.Rows) == p.Limit {
			// The row past the page: it exists, which is all it is read for.
			out.More = true
			break
		}
		v, err := scan(op, rows)
		if err != nil {
			return Paged[T]{}, err
		}
		out.Rows = append(out.Rows, v)
	}
	if err := rows.Err(); err != nil {
		return Paged[T]{}, wrap(op, err)
	}
	return out, nil
}
