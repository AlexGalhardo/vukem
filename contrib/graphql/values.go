package graphql

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// Coerced input values are nil, bool, int (Int), float64 (Float), string
// (String, ID, enums), []any, map[string]any, or what a custom scalar's
// Parse returns.

// errAbsent marks a variable that was not provided (distinct from null).
var errAbsent = errors.New("absent")

func describeLiteral(v *value) string {
	switch v.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case valString:
		return strconv.Quote(v.raw)
	case valVariable:
		return "$" + v.raw
	case valList:
		return "list"
	case valObject:
		return "object"
	}

	return v.raw
}

// coerceLiteral converts an AST value to t (spec §5.6.1 rules applied to
// literals). vars holds coerced variable values; nil in constant context.
func coerceLiteral(v *value, t *gtype, vars map[string]any) (any, error) {
	if v.kind == valVariable {
		val, ok := vars[v.raw]
		if !ok {
			return nil, errAbsent
		}

		if val == nil && t.kind == kindNonNull {
			return nil, fmt.Errorf("expected non-null value of type %s, found null from $%s", t, v.raw)
		}

		return val, nil
	}

	if t.kind == kindNonNull {
		if v.kind == valNull {
			return nil, fmt.Errorf("expected value of type %s, found null", t)
		}

		return coerceLiteral(v, t.ofType, vars)
	}

	if v.kind == valNull {
		return nil, nil
	}

	if t.kind == kindList {
		if v.kind != valList {
			item, err := coerceLiteral(v, t.ofType, vars)
			if errors.Is(err, errAbsent) {
				return []any{nil}, nil
			}

			if err != nil {
				return nil, err
			}

			return []any{item}, nil
		}

		out := make([]any, 0, len(v.list))

		for i, it := range v.list {
			item, err := coerceLiteral(it, t.ofType, vars)
			if errors.Is(err, errAbsent) {
				if t.ofType.kind == kindNonNull {
					return nil, fmt.Errorf("[%d]: expected non-null value of type %s", i, t.ofType)
				}

				item, err = nil, nil
			}

			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}

			out = append(out, item)
		}

		return out, nil
	}

	n := t.named
	switch n.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case kindInputObject:
		return coerceObjectLiteral(v, n, vars)
	case kindEnum:
		if v.kind != valEnum {
			return nil, fmt.Errorf("enum %q cannot represent non-enum value: %s", n.name, describeLiteral(v))
		}

		if n.enumMap[v.raw] == nil {
			return nil, fmt.Errorf("value %q does not exist in %q enum", v.raw, n.name)
		}

		return v.raw, nil
	}

	return scalarLiteral(v, n, vars)
}

func coerceObjectLiteral(v *value, n *namedType, vars map[string]any) (any, error) {
	if v.kind != valObject {
		return nil, fmt.Errorf("expected value of type %q, found %s", n.name, describeLiteral(v))
	}

	given := map[string]*value{}

	for _, f := range v.fields {
		if n.inputMap[f.name] == nil {
			return nil, fmt.Errorf("field %q is not defined by type %q", f.name, n.name)
		}

		if given[f.name] != nil {
			return nil, fmt.Errorf("there can be only one input field named %q", f.name)
		}

		given[f.name] = f.value
	}

	out := map[string]any{}

	for _, iv := range n.inputs {
		lit := given[iv.name]

		var (
			val any
			err error
		)

		if lit == nil {
			err = errAbsent
		} else {
			val, err = coerceLiteral(lit, iv.typ, vars)
		}

		if errors.Is(err, errAbsent) {
			switch {
			case iv.hasDefault:
				out[iv.name] = iv.defValue
			case iv.typ.kind == kindNonNull:
				return nil, fmt.Errorf("field %q of required type %s was not provided", n.name+"."+iv.name, iv.typ)
			}

			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%s: %w", iv.name, err)
		}

		out[iv.name] = val
	}

	return out, nil
}

func scalarLiteral(v *value, n *namedType, vars map[string]any) (any, error) {
	switch n.name {
	case "Int":
		if v.kind != valInt {
			return nil, fmt.Errorf("int cannot represent non-integer value: %s", describeLiteral(v))
		}

		i, err := strconv.ParseInt(v.raw, 10, 64)
		if err != nil || i > math.MaxInt32 || i < math.MinInt32 {
			return nil, fmt.Errorf("int cannot represent non 32-bit signed integer value: %s", v.raw)
		}

		return int(i), nil
	case "Float":
		if v.kind != valInt && v.kind != valFloat {
			return nil, fmt.Errorf("float cannot represent non numeric value: %s", describeLiteral(v))
		}

		f, err := strconv.ParseFloat(v.raw, 64)
		if err != nil || math.IsInf(f, 0) {
			return nil, fmt.Errorf("float cannot represent value: %s", v.raw)
		}

		return f, nil
	case "String":
		if v.kind != valString {
			return nil, fmt.Errorf("string cannot represent a non string value: %s", describeLiteral(v))
		}

		return v.raw, nil
	case "Boolean":
		if v.kind != valBoolean {
			return nil, fmt.Errorf("boolean cannot represent a non boolean value: %s", describeLiteral(v))
		}

		return v.raw == "true", nil
	case "ID":
		if v.kind != valString && v.kind != valInt {
			return nil, fmt.Errorf("id cannot represent a non-string and non-integer value: %s", describeLiteral(v))
		}

		return v.raw, nil
	}

	generic, err := literalToGeneric(v, vars)
	if err != nil {
		return nil, err
	}

	return parseCustom(n, generic)
}

func parseCustom(n *namedType, v any) (any, error) {
	if n.scalar == nil || n.scalar.Parse == nil {
		return v, nil
	}

	out, err := n.scalar.Parse(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", n.name, err)
	}

	return out, nil
}

// literalToGeneric converts a literal to plain Go values for custom
// scalars (which may accept objects or lists).
func literalToGeneric(v *value, vars map[string]any) (any, error) {
	switch v.kind {
	case valVariable:
		val, ok := vars[v.raw]
		if !ok {
			return nil, nil
		}

		return val, nil
	case valInt:
		i, err := strconv.ParseInt(v.raw, 10, 64)
		if err != nil {
			return strconv.ParseFloat(v.raw, 64)
		}

		return i, nil
	case valFloat:
		return strconv.ParseFloat(v.raw, 64)
	case valString, valEnum:
		return v.raw, nil
	case valBoolean:
		return v.raw == "true", nil
	case valNull:
		return nil, nil
	case valList:
		out := make([]any, 0, len(v.list))

		for _, it := range v.list {
			g, err := literalToGeneric(it, vars)
			if err != nil {
				return nil, err
			}

			out = append(out, g)
		}

		return out, nil
	default:
		out := make(map[string]any, len(v.fields))

		for _, f := range v.fields {
			g, err := literalToGeneric(f.value, vars)
			if err != nil {
				return nil, err
			}

			out[f.name] = g
		}

		return out, nil
	}
}

// coerceVariable converts a JSON (or Go) variable value to t (spec
// §5.6.1 CoerceVariableValues / input coercion).
func coerceVariable(v any, t *gtype) (any, error) {
	if t.kind == kindNonNull {
		if v == nil {
			return nil, fmt.Errorf("expected non-nullable type %q not to be null", t)
		}

		return coerceVariable(v, t.ofType)
	}

	if v == nil {
		return nil, nil
	}

	if t.kind == kindList {
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			item, err := coerceVariable(v, t.ofType)
			if err != nil {
				return nil, err
			}

			return []any{item}, nil
		}

		out := make([]any, rv.Len())

		for i := range rv.Len() {
			item, err := coerceVariable(rv.Index(i).Interface(), t.ofType)
			if err != nil {
				return nil, fmt.Errorf("at index %d: %w", i, err)
			}

			out[i] = item
		}

		return out, nil
	}

	n := t.named

	switch n.kind { //nolint:exhaustive // the remaining kinds take the common path below
	case kindInputObject:
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected type %q to be an object", n.name)
		}

		for _, k := range sortedKeys(m) {
			if n.inputMap[k] == nil {
				return nil, fmt.Errorf("field %q is not defined by type %q", k, n.name)
			}
		}

		out := map[string]any{}

		for _, iv := range n.inputs {
			fv, present := m[iv.name]

			switch {
			case !present && iv.hasDefault:
				out[iv.name] = iv.defValue
			case !present && iv.typ.kind == kindNonNull:
				return nil, fmt.Errorf("field %q of required type %q was not provided", iv.name, iv.typ)
			case present:
				c, err := coerceVariable(fv, iv.typ)
				if err != nil {
					return nil, fmt.Errorf("at %q: %w", iv.name, err)
				}

				out[iv.name] = c
			}
		}

		return out, nil
	case kindEnum:
		s, ok := v.(string)
		if !ok || n.enumMap[s] == nil {
			return nil, fmt.Errorf("value %s does not exist in %q enum", jsonText(v), n.name)
		}

		return s, nil
	}

	return scalarInput(n, v)
}

func scalarInput(n *namedType, v any) (any, error) {
	switch n.name {
	case "Int":
		i, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("int cannot represent non-integer value: %s", jsonText(v))
		}

		if i > math.MaxInt32 || i < math.MinInt32 {
			return nil, fmt.Errorf("int cannot represent non 32-bit signed integer value: %s", jsonText(v))
		}

		return int(i), nil
	case "Float":
		f, ok := toFloat(v)
		if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("float cannot represent non numeric value: %s", jsonText(v))
		}

		return f, nil
	case "String":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("string cannot represent a non string value: %s", jsonText(v))
		}

		return s, nil
	case "Boolean":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("boolean cannot represent a non boolean value: %s", jsonText(v))
		}

		return b, nil
	case "ID":
		if s, ok := v.(string); ok {
			return s, nil
		}

		if i, ok := toInt(v); ok {
			return strconv.FormatInt(i, 10), nil
		}

		return nil, fmt.Errorf("id cannot represent value: %s", jsonText(v))
	}

	return parseCustom(n, normalizeJSON(v))
}

// normalizeJSON turns json.Number into int64 or float64, recursively.
func normalizeJSON(v any) any {
	switch v := v.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}

		f, _ := v.Float64()

		return f
	case []any:
		out := make([]any, len(v))
		for i, it := range v {
			out[i] = normalizeJSON(it)
		}

		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, it := range v {
			out[k] = normalizeJSON(it)
		}

		return out
	}

	return v
}

func toInt(v any) (int64, bool) {
	switch v := v.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true
		}

		f, err := v.Float64()
		if err != nil || f != math.Trunc(f) || math.Abs(f) > math.MaxInt64 {
			return 0, false
		}

		return int64(f), true
	case float64:
		if v != math.Trunc(v) || math.Abs(v) > math.MaxInt64 {
			return 0, false
		}

		return int64(v), true
	case float32:
		return toInt(float64(v))
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() { //nolint:exhaustive // the remaining kinds take the common path below
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if rv.Uint() > math.MaxInt64 {
			return 0, false
		}

		return int64(rv.Uint()), true //nolint:gosec // bounded by the check above
	}

	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case float32:
		return float64(v), true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() { //nolint:exhaustive // the remaining kinds take the common path below
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		i, ok := toInt(v)
		return float64(i), ok
	}

	return 0, false
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}

	if len(b) > 64 {
		return string(b[:61]) + "..."
	}

	return string(b)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// serializeLeaf converts a resolver value for a scalar or enum field
// (spec §3.5 result coercion).
func serializeLeaf(n *namedType, v any) (any, error) {
	if n.kind == kindEnum {
		s, ok := stringValue(v)
		if !ok || n.enumMap[s] == nil {
			return nil, fmt.Errorf("enum %q cannot represent value: %s", n.name, jsonText(v))
		}

		return s, nil
	}

	switch n.name {
	case "Int":
		i, ok := toInt(v)
		if !ok {
			if b, isBool := v.(bool); isBool {
				return boolInt(b), nil
			}

			return nil, fmt.Errorf("int cannot represent non-integer value: %s", jsonText(v))
		}

		if i > math.MaxInt32 || i < math.MinInt32 {
			return nil, fmt.Errorf("int cannot represent non 32-bit signed integer value: %s", jsonText(v))
		}

		return i, nil
	case "Float":
		f, ok := toFloat(v)
		if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("float cannot represent non numeric value: %s", jsonText(v))
		}

		return f, nil
	case "String":
		if s, ok := stringValue(v); ok {
			return s, nil
		}

		switch rv := reflect.ValueOf(v); rv.Kind() { //nolint:exhaustive // the remaining kinds take the common path below
		case reflect.Bool:
			return strconv.FormatBool(rv.Bool()), nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return fmt.Sprint(v), nil
		}

		return nil, fmt.Errorf("string cannot represent value: %s", jsonText(v))
	case "Boolean":
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Bool {
			return rv.Bool(), nil
		}

		return nil, fmt.Errorf("boolean cannot represent a non boolean value: %s", jsonText(v))
	case "ID":
		if s, ok := stringValue(v); ok {
			return s, nil
		}

		if i, ok := toInt(v); ok {
			return strconv.FormatInt(i, 10), nil
		}

		return nil, fmt.Errorf("id cannot represent value: %s", jsonText(v))
	}

	if n.scalar != nil && n.scalar.Serialize != nil {
		out, err := n.scalar.Serialize(v)
		if err != nil {
			return nil, fmt.Errorf("%s cannot represent value: %w", n.name, err)
		}

		return out, nil
	}

	return v, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}

	return 0
}

// stringValue accepts strings, named string types and fmt.Stringer.
func stringValue(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case fmt.Stringer:
		return v.String(), true
	}

	if rv := reflect.ValueOf(v); rv.Kind() == reflect.String {
		return rv.String(), true
	}

	return "", false
}
