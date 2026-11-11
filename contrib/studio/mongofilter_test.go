package studio

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMongoFilter(t *testing.T) {
	t.Parallel()

	oid, err := bson.ObjectIDFromHex("507f1f77bcf86cd799439011")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		filters []Filter
		want    bson.D
	}{
		{"none", nil, bson.D{}},
		{
			"eq number",
			[]Filter{{"age", "eq", "30"}},
			bson.D{{Key: "age", Value: bson.D{{Key: "$eq", Value: int64(30)}}}},
		},
		{"eq float", []Filter{{"price", "eq", "1.5"}}, bson.D{{Key: "price", Value: bson.D{{Key: "$eq", Value: 1.5}}}}},
		{"eq bool", []Filter{{"ok", "eq", "true"}}, bson.D{{Key: "ok", Value: bson.D{{Key: "$eq", Value: true}}}}},
		{
			"eq quoted string",
			[]Filter{{"name", "eq", `"42"`}},
			bson.D{{Key: "name", Value: bson.D{{Key: "$eq", Value: "42"}}}},
		},
		{
			"eq plain string",
			[]Filter{{"name", "eq", "ada"}},
			bson.D{{Key: "name", Value: bson.D{{Key: "$eq", Value: "ada"}}}},
		},
		{"ne", []Filter{{"a.b", "ne", "x"}}, bson.D{{Key: "a.b", Value: bson.D{{Key: "$ne", Value: "x"}}}}},
		{"lt", []Filter{{"n", "lt", "1"}}, bson.D{{Key: "n", Value: bson.D{{Key: "$lt", Value: int64(1)}}}}},
		{"lte", []Filter{{"n", "lte", "1"}}, bson.D{{Key: "n", Value: bson.D{{Key: "$lte", Value: int64(1)}}}}},
		{"gt", []Filter{{"n", "gt", "1"}}, bson.D{{Key: "n", Value: bson.D{{Key: "$gt", Value: int64(1)}}}}},
		{"gte", []Filter{{"n", "gte", "1"}}, bson.D{{Key: "n", Value: bson.D{{Key: "$gte", Value: int64(1)}}}}},
		{"null", []Filter{{"n", "null", ""}}, bson.D{{Key: "n", Value: nil}}},
		{"notnull", []Filter{{"n", "notnull", ""}}, bson.D{{Key: "n", Value: bson.D{{Key: "$ne", Value: nil}}}}},
		{
			"contains",
			[]Filter{{"n", "contains", "a.b"}},
			bson.D{{Key: "n", Value: bson.D{{Key: "$regex", Value: `a\.b`}}}},
		},
		{"starts", []Filter{{"n", "starts", "a+"}}, bson.D{{Key: "n", Value: bson.D{{Key: "$regex", Value: `^a\+`}}}}},
		{
			"_id object id",
			[]Filter{{"_id", "eq", "507f1f77bcf86cd799439011"}},
			bson.D{{Key: "_id", Value: bson.D{{Key: "$eq", Value: oid}}}},
		},
		{
			"object id text outside _id stays text",
			[]Filter{{"ref", "eq", "507f1f77bcf86cd799439011"}},
			bson.D{{Key: "ref", Value: bson.D{{Key: "$eq", Value: "507f1f77bcf86cd799439011"}}}},
		},
		{"two filters are anded", []Filter{{"a", "eq", "1"}, {"b", "eq", "2"}}, bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "a", Value: bson.D{{Key: "$eq", Value: int64(1)}}}},
			bson.D{{Key: "b", Value: bson.D{{Key: "$eq", Value: int64(2)}}}},
		}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := buildMongoFilter(tt.filters)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("filter (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSecurity_A03_StudioMongoRejectsOperatorFields(t *testing.T) {
	t.Parallel()

	for _, col := range []string{"$where", "a.$b", "a b", "", "$ne", "a..b", ".a", "a.", "a;b", "a\nb"} {
		if _, err := buildMongoFilter([]Filter{{col, "eq", "1"}}); err == nil {
			t.Errorf("column %q accepted", col)
		}
	}

	if _, err := buildMongoFilter([]Filter{{"a", "$where", "1"}}); err == nil {
		t.Error("an operator outside the fixed table was accepted")
	}
}

func TestSecurity_A03_StudioMongoRejectsObjectValues(t *testing.T) {
	t.Parallel()

	for _, v := range []string{`{"$ne":null}`, `[1]`, ` {"a":1}`, `[`} {
		if _, err := buildMongoFilter([]Filter{{"a", "eq", v}}); err == nil {
			t.Errorf("value %q accepted: an object or array would put operators in the client's hands", v)
		}
	}
}

func TestSecurity_A03_StudioMongoEscapesRegex(t *testing.T) {
	t.Parallel()

	got, err := buildMongoFilter([]Filter{{"n", "contains", ".*"}})
	if err != nil {
		t.Fatal(err)
	}

	cond, _ := got[0].Value.(bson.D)
	if len(cond) != 1 || cond[0].Value != `\.\*` {
		t.Errorf("contains .* = %v, want the literal regex \\.\\*", got)
	}
}

func TestSecurity_A03_StudioMongoBlocksServerJS(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"depth 1":     `{"$where": "1"}`,
		"depth 2":     `{"a": {"$function": {"body": "x", "args": [], "lang": "js"}}}`,
		"in array":    `{"$and": [{"b": 1}, {"$where": "x"}]}`,
		"accumulator": `{"x": {"$accumulator": {}}}`,
	}

	for name, src := range docs {
		for _, write := range []bool{false, true} {
			v, err := decodeConsoleDoc(src)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}

			if err := checkConsoleValue(v, write); err == nil {
				t.Errorf("%s (write=%v): accepted", name, write)
			}
		}
	}

	pipeline := `[{"$match": {}}, {"$group": {"_id": 1, "x": {"$accumulator": {}}}}]`

	v, err := decodePipeline(pipeline)
	if err != nil {
		t.Fatal(err)
	}

	if err := checkConsoleValue(v, true); err == nil {
		t.Error("$accumulator inside a pipeline stage accepted")
	}
}

func TestSecurity_A01_StudioMongoBlocksWriteStagesReadOnly(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{`{"$out": "c"}`, `{"$merge": {"into": "c"}}`} {
		v, err := decodePipeline(`[{"$match": {}}, ` + stage + `]`)
		if err != nil {
			t.Fatal(err)
		}

		if err := checkConsoleValue(v, false); err == nil || !strings.Contains(err.Error(), "--write") {
			t.Errorf("%s without write = %v, want a refusal that mentions --write", stage, err)
		}

		if err := checkConsoleValue(v, true); err != nil {
			t.Errorf("%s with write = %v", stage, err)
		}
	}
}

func TestMongoConsoleDecoding(t *testing.T) {
	t.Parallel()

	if _, err := decodeConsoleDoc(`{"a": {"$oid": "507f1f77bcf86cd799439011"}}`); err != nil {
		t.Errorf("Extended JSON ObjectId: %v", err)
	}

	for _, bad := range []string{``, `[1]`, `not json`, `{"a":`} {
		if _, err := decodeConsoleDoc(bad); err == nil {
			t.Errorf("decodeConsoleDoc(%q) accepted", bad)
		}
	}

	// A pipeline text cannot smuggle extra top-level keys through the wrapper.
	for _, bad := range []string{`[], "extra": 1`, `{}`, `1`, ``} {
		if _, err := decodePipeline(bad); err == nil {
			t.Errorf("decodePipeline(%q) accepted", bad)
		}
	}

	if _, err := decodePipeline(`[{"$match": {}}]`); err != nil {
		t.Errorf("a plain pipeline: %v", err)
	}
}

func TestMongoIDRoundTrip(t *testing.T) {
	t.Parallel()

	oid, _ := bson.ObjectIDFromHex("507f1f77bcf86cd799439011")

	for _, id := range []any{oid, "abc", int32(7), int64(1 << 40)} {
		text, err := idText(id)
		if err != nil {
			t.Fatal(err)
		}

		back, err := parseID(text)
		if err != nil || cmp.Diff(id, back) != "" {
			t.Errorf("id %v -> %q -> %v, %v", id, text, back, err)
		}
	}

	for _, bad := range []string{``, `{"$oid":"x"}`, `1, "x": 2`, `{"$gt": 1}`} {
		if _, err := parseID(bad); err == nil {
			t.Errorf("parseID(%q) accepted", bad)
		}
	}
}
