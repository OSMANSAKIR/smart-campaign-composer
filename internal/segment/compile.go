package segment

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"smartcampaign/internal/catalog"
)

// ErrUnvalidated is returned when Compile receives a ValidRule that Validate did not produce.
var ErrUnvalidated = errors.New("segment: Compile called with an unvalidated rule")

// Compile turns a validated rule into a WHERE fragment over `users u` with placeholders starting at
// $firstParam, plus the matching arguments. SQL text comes only from catalog.Field.SQL and fixed operator
// templates; every value is a bind parameter (segment-dsl.md §3). Zero groups compile to TRUE.
func Compile(v ValidRule, firstParam int) (where string, args []any, err error) {
	if !v.ok {
		return "", nil, ErrUnvalidated
	}
	if firstParam < 1 {
		return "", nil, fmt.Errorf("segment: firstParam must be >= 1, got %d", firstParam)
	}
	if len(v.conds) == 0 {
		return "TRUE", nil, nil
	}
	c := compiler{next: firstParam}
	groups := make([]string, 0, len(v.conds))
	for _, g := range v.conds {
		parts := make([]string, 0, len(g))
		for _, cd := range g {
			sql, err := c.cond(cd)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, sql)
		}
		groups = append(groups, "("+strings.Join(parts, " OR ")+")")
	}
	return strings.Join(groups, " AND "), c.args, nil
}

type compiler struct {
	next int
	args []any
}

func (c *compiler) param(v any) string {
	c.args = append(c.args, v)
	p := "$" + strconv.Itoa(c.next)
	c.next++
	return p
}

// numberArg keeps integer fields integral so Postgres compares int to int.
func numberArg(f catalog.Field, x float64) any {
	if f.Integer {
		return int64(x)
	}
	return x
}

var numberOps = map[catalog.Op]string{
	catalog.OpEq: "=", catalog.OpNeq: "<>", catalog.OpGt: ">", catalog.OpGte: ">=", catalog.OpLt: "<", catalog.OpLte: "<=",
}

func (c *compiler) cond(cd cond) (string, error) {
	expr := cd.field.SQL // whitelisted catalog expression; never model-controlled
	switch cd.field.Type {
	case catalog.Number:
		if cd.op == catalog.OpBetween {
			lo := c.param(numberArg(cd.field, cd.lo))
			hi := c.param(numberArg(cd.field, cd.hi))
			return fmt.Sprintf("%s BETWEEN %s AND %s", expr, lo, hi), nil
		}
		if sym, ok := numberOps[cd.op]; ok {
			return fmt.Sprintf("%s %s %s", expr, sym, c.param(numberArg(cd.field, cd.num))), nil
		}
	case catalog.Enum:
		switch cd.op {
		case catalog.OpEq:
			return fmt.Sprintf("%s = %s", expr, c.param(cd.str)), nil
		case catalog.OpNeq:
			return fmt.Sprintf("%s <> %s", expr, c.param(cd.str)), nil
		case catalog.OpIn:
			return fmt.Sprintf("%s = ANY(%s)", expr, c.param(cd.strs)), nil
		case catalog.OpNotIn:
			return fmt.Sprintf("%s <> ALL(%s)", expr, c.param(cd.strs)), nil
		}
	case catalog.Bool:
		switch cd.op {
		case catalog.OpIsTrue:
			return expr + " IS TRUE", nil
		case catalog.OpIsFalse:
			return expr + " IS FALSE", nil
		}
	case catalog.Date:
		switch cd.op {
		case catalog.OpWithinLastDays:
			return fmt.Sprintf("%s >= now() - make_interval(days => %s)", expr, c.param(int32(cd.days))), nil
		case catalog.OpNotWithinLastDays:
			return fmt.Sprintf("(%s IS NULL OR %s < now() - make_interval(days => %s))", expr, expr, c.param(int32(cd.days))), nil
		case catalog.OpBefore:
			return fmt.Sprintf("%s < (%s::date)::timestamp AT TIME ZONE 'Europe/Istanbul'", expr, c.param(dateArg(cd.date))), nil
		case catalog.OpAfter:
			return fmt.Sprintf("%s >= (%s::date + 1)::timestamp AT TIME ZONE 'Europe/Istanbul'", expr, c.param(dateArg(cd.date))), nil
		case catalog.OpIsNull:
			return expr + " IS NULL", nil
		case catalog.OpIsNotNull:
			return expr + " IS NOT NULL", nil
		}
	}
	// Fail closed: a catalog operator the compiler does not know never becomes SQL.
	return "", fmt.Errorf("segment: no SQL template for %s %s", cd.field.Type, cd.op)
}

// dateArg passes a validated YYYY-MM-DD as a time.Time so pgx encodes it as a date, not as text.
func dateArg(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s) // validated by Validate
	return t
}
