package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// fieldRe is a field path: names joined by dots, never starting with "$".
var fieldRe = regexp.MustCompile(
	`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)*$`,
) //nolint:gochecknoglobals // compiled once

var hexID = regexp.MustCompile(`^[0-9a-fA-F]{24}$`) //nolint:gochecknoglobals // compiled once

// comparison maps a grid operator to its MongoDB operator.
var comparison = map[string]string{ //nolint:gochecknoglobals // read-only lookup table
	"eq": "$eq", "ne": "$ne", "lt": "$lt", "lte": "$lte", "gt": "$gt", "gte": "$gte",
}

// buildMongoFilter turns the grid's conditions into a filter built on the
// server: the client sends only a field path, an operator from a fixed table
// and a scalar, never a MongoDB operator or an object.
func buildMongoFilter(filters []Filter) (bson.D, error) {
	conds := make(bson.A, 0, len(filters))

	for _, f := range filters {
		c, err := oneCondition(f)
		if err != nil {
			return nil, err
		}

		conds = append(conds, c)
	}

	switch len(conds) {
	case 0:
		return bson.D{}, nil
	case 1:
		one, _ := conds[0].(bson.D) // oneCondition returned a bson.D

		return one, nil
	default:
		return bson.D{{Key: "$and", Value: conds}}, nil
	}
}

func oneCondition(f Filter) (bson.D, error) {
	if !fieldRe.MatchString(f.Column) {
		return nil, errors.New("studio: invalid field name")
	}

	switch f.Op {
	case "null":
		return bson.D{{Key: f.Column, Value: nil}}, nil
	case "notnull":
		return bson.D{{Key: f.Column, Value: bson.D{{Key: "$ne", Value: nil}}}}, nil
	case "contains":
		return regexCond(f.Column, regexp.QuoteMeta(f.Value)), nil
	case "starts":
		return regexCond(f.Column, "^"+regexp.QuoteMeta(f.Value)), nil
	}

	op, ok := comparison[f.Op]
	if !ok {
		return nil, errors.New("studio: unknown filter operator")
	}

	v, err := scalar(f.Column, f.Value)
	if err != nil {
		return nil, err
	}

	return bson.D{{Key: f.Column, Value: bson.D{{Key: op, Value: v}}}}, nil
}

func regexCond(field, pattern string) bson.D {
	return bson.D{{Key: field, Value: bson.D{{Key: "$regex", Value: pattern}}}}
}

// scalar reads a filter value: a JSON number, boolean or quoted string is
// taken as such; 24 hex digits on _id are an ObjectID; anything else is text.
// An object or an array is refused: it could carry operators.
func scalar(field, raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return nil, errors.New("studio: a filter value must be a scalar, not an object or an array")
	}

	if field == "_id" && hexID.MatchString(trimmed) {
		return bson.ObjectIDFromHex(trimmed)
	}

	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err == nil && !dec.More() {
		switch x := v.(type) {
		case json.Number:
			if i, err := x.Int64(); err == nil {
				return i, nil
			}

			if f, err := x.Float64(); err == nil {
				return f, nil
			}
		case bool, string:
			return x, nil
		}
	}

	return raw, nil
}

// blockedAlways are server-side JavaScript operators, refused everywhere.
var blockedAlways = map[string]bool{
	"$where":       true,
	"$function":    true,
	"$accumulator": true,
} //nolint:gochecknoglobals // read-only

// writeStages persist data, so they need --write.
var writeStages = map[string]bool{"$out": true, "$merge": true} //nolint:gochecknoglobals // read-only

// decodeConsoleDoc decodes the Extended JSON object of a find.
func decodeConsoleDoc(src string) (bson.D, error) {
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(src), false, &d); err != nil {
		return nil, fmt.Errorf("studio: not a valid Extended JSON object: %w", err)
	}

	return d, nil
}

// decodePipeline decodes the Extended JSON array of an aggregate. The array
// is wrapped in an object only to reach the decoder; anything but one array
// is refused.
func decodePipeline(src string) (bson.A, error) {
	var wrapper bson.D
	if err := bson.UnmarshalExtJSON([]byte(`{"p":`+src+`}`), false, &wrapper); err != nil {
		return nil, fmt.Errorf("studio: not a valid Extended JSON array: %w", err)
	}

	if len(wrapper) != 1 {
		return nil, errors.New("studio: a pipeline is one array")
	}

	stages, ok := wrapper[0].Value.(bson.A)
	if !ok {
		return nil, errors.New("studio: a pipeline is an array of stages")
	}

	for _, s := range stages {
		if _, ok := s.(bson.D); !ok {
			return nil, errors.New("studio: every stage must be an object")
		}
	}

	return stages, nil
}

// checkConsoleValue walks a decoded console document or pipeline and refuses
// server-side JavaScript always, and $out and $merge unless write is set.
func checkConsoleValue(v any, write bool) error {
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			if blockedAlways[e.Key] {
				return fmt.Errorf("studio: %s is not allowed (server-side JavaScript)", e.Key)
			}

			if writeStages[e.Key] && !write {
				return fmt.Errorf("studio: %s writes data and needs --write", e.Key)
			}

			if err := checkConsoleValue(e.Value, write); err != nil {
				return err
			}
		}
	case bson.A:
		for _, e := range x {
			if err := checkConsoleValue(e, write); err != nil {
				return err
			}
		}
	}

	return nil
}

// idText is the _id as canonical Extended JSON, which round-trips every type.
func idText(id any) (string, error) {
	b, err := bson.MarshalExtJSON(bson.D{{Key: "_id", Value: id}}, true, false)
	if err != nil {
		return "", err
	}

	b = bytes.TrimSpace(b)
	if !bytes.HasPrefix(b, []byte(`{"_id":`)) || !bytes.HasSuffix(b, []byte("}")) {
		return "", errors.New("studio: unexpected _id encoding")
	}

	return string(b[len(`{"_id":`) : len(b)-1]), nil
}

// parseID reads an _id from the URL. Only scalar types are accepted: an
// object would be an operator document in the filter {_id: ...}.
func parseID(text string) (any, error) {
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(`{"_id":`+text+`}`), false, &d); err != nil {
		return nil, errors.New("studio: invalid _id")
	}

	if len(d) != 1 || d[0].Key != "_id" {
		return nil, errors.New("studio: invalid _id")
	}

	switch d[0].Value.(type) {
	case bson.ObjectID, string, int32, int64, float64, bson.Binary, bson.DateTime, bson.Decimal128:
		return d[0].Value, nil
	default:
		return nil, errors.New("studio: _id must be a scalar")
	}
}
