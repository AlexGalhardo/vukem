package z

import (
	"maps"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// issueList is the comparable form of the issues of err: path, code, key and
// params, in order.
type issueRow struct {
	Path, Code, Key string
	Params          map[string]any
}

func issueRows(t *testing.T, err error) []issueRow {
	t.Helper()

	is, ok := AsIssues(err)
	if !ok {
		t.Fatalf("error = %v, want Issues", err)
	}

	rows := make([]issueRow, len(is))
	for i, is := range is {
		rows[i] = issueRow{is.Path, is.Code, is.Key, is.Params}
	}

	return rows
}

type signup struct {
	Email    string  `json:"email"`
	Password string  `json:"password"`
	Kind     string  `json:"kind"`
	TaxID    *string `json:"tax_id"`
	Phone    *string `json:"phone"`
	Fax      *string `json:"fax"`
}

func signupSchema() StructSchema[signup] {
	return Struct[signup](Shape{
		"email":    String().Email(),
		"password": String().Min(8),
		"kind":     Enum("person", "company"),
		"tax_id":   String().Optional(),
		"phone":    String().Optional(),
		"fax":      String().Optional(),
	})
}

func okSignup() map[string]any {
	return map[string]any{"email": "a@b.co", "password": "longenough", "kind": "person"}
}

func with(base map[string]any, kv ...any) map[string]any {
	out := maps.Clone(base)

	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}

	return out
}

func TestStructRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema StructSchema[signup]
		in     map[string]any
		want   []issueRow
	}{
		{
			"confirmed matches", signupSchema().Confirmed("password"),
			with(okSignup(), "password_confirmation", "longenough"), nil,
		},
		{
			"confirmed differs", signupSchema().Confirmed("password"),
			with(okSignup(), "password_confirmation", "other"),
			[]issueRow{{"password", CodeCustom, "confirmed", nil}},
		},
		{
			"confirmed missing counts as different", signupSchema().Confirmed("password"), okSignup(),
			[]issueRow{{"password", CodeCustom, "confirmed", nil}},
		},
		{
			"confirmed skipped when the field failed", signupSchema().Confirmed("password"),
			with(okSignup(), "password", "short", "password_confirmation", "other"),
			[]issueRow{{"password", CodeTooSmall, "too_small.string", map[string]any{"min": 8}}},
		},

		{"same passes", signupSchema().SameAs("phone", "fax"), with(okSignup(), "phone", "1", "fax", "1"), nil},
		{
			"same fails", signupSchema().SameAs("phone", "fax"), with(okSignup(), "phone", "1", "fax", "2"),
			[]issueRow{{"phone", CodeCustom, "same", map[string]any{"other": "fax"}}},
		},
		{"same skipped when one is absent", signupSchema().SameAs("phone", "fax"), with(okSignup(), "phone", "1"), nil},
		{"different passes", signupSchema().NotSameAs("phone", "fax"), with(okSignup(), "phone", "1", "fax", "2"), nil},
		{
			"different fails", signupSchema().NotSameAs("phone", "fax"), with(okSignup(), "phone", "1", "fax", "1"),
			[]issueRow{{"phone", CodeCustom, "different", map[string]any{"other": "fax"}}},
		},

		{
			"required_if passes when the value differs",
			signupSchema().RequiredIf("tax_id", "kind", "company"),
			okSignup(),
			nil,
		},
		{
			"required_if passes when sent", signupSchema().RequiredIf("tax_id", "kind", "company"),
			with(okSignup(), "kind", "company", "tax_id", "123"), nil,
		},
		{
			"required_if fails at the key, not at the other", signupSchema().RequiredIf("tax_id", "kind", "company"),
			with(okSignup(), "kind", "company"),
			[]issueRow{{"tax_id", CodeRequired, "required_if", map[string]any{"other": "kind", "value": "company"}}},
		},
		{
			"required_if with an empty string", signupSchema().RequiredIf("tax_id", "kind", "company"),
			with(okSignup(), "kind", "company", "tax_id", ""),
			[]issueRow{{"tax_id", CodeRequired, "required_if", map[string]any{"other": "kind", "value": "company"}}},
		},
		{
			"required_if skipped when the other failed", signupSchema().RequiredIf("tax_id", "kind", "company"),
			with(okSignup(), "kind", "robot"),
			[]issueRow{{"kind", CodeInvalidEnum, CodeInvalidEnum, map[string]any{"options": "person, company"}}},
		},

		{
			"required_when", signupSchema().RequiredWhen("tax_id", func(s signup) bool { return s.Kind == "company" }),
			with(okSignup(), "kind", "company"),
			[]issueRow{{"tax_id", CodeRequired, CodeRequired, nil}},
		},

		{
			"required_if_exists needs all", signupSchema().RequiredIfExists("fax", "phone", "tax_id"),
			with(okSignup(), "phone", "1"), nil,
		},
		{
			"required_if_exists", signupSchema().RequiredIfExists("fax", "phone", "tax_id"),
			with(okSignup(), "phone", "1", "tax_id", "2"),
			[]issueRow{{"fax", CodeRequired, "required_with", map[string]any{"others": "phone, tax id"}}},
		},
		{
			"required_if_any_exists", signupSchema().RequiredIfAnyExists("fax", "phone", "tax_id"),
			with(okSignup(), "phone", "1"),
			[]issueRow{{"fax", CodeRequired, "required_with", map[string]any{"others": "phone, tax id"}}},
		},
		{
			"required_if_any_exists none sent",
			signupSchema().RequiredIfAnyExists("fax", "phone", "tax_id"),
			okSignup(),
			nil,
		},
		{
			"required_if_missing needs none", signupSchema().RequiredIfMissing("fax", "phone", "tax_id"),
			okSignup(),
			[]issueRow{{"fax", CodeRequired, "required_without", map[string]any{"others": "phone, tax id"}}},
		},
		{
			"required_if_missing one sent", signupSchema().RequiredIfMissing("fax", "phone", "tax_id"),
			with(okSignup(), "phone", "1"), nil,
		},
		{
			"required_if_any_missing", signupSchema().RequiredIfAnyMissing("fax", "phone", "tax_id"),
			with(okSignup(), "phone", "1"),
			[]issueRow{{"fax", CodeRequired, "required_without", map[string]any{"others": "phone, tax id"}}},
		},
		{
			"required_if_any_missing all sent", signupSchema().RequiredIfAnyMissing("fax", "phone", "tax_id"),
			with(okSignup(), "phone", "1", "tax_id", "2"), nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.schema.Parse(tt.in)

			var got []issueRow
			if err != nil {
				got = issueRows(t, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("issues (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStructRulesBootChecks(t *testing.T) {
	t.Parallel()

	base := signupSchema()

	mustPanic(t, "Confirmed of an unknown key", func() { base.Confirmed("nope") })
	mustPanic(t, "SameAs of an unknown key", func() { base.SameAs("phone", "nope") })
	mustPanic(t, "SameAs of different types", func() {
		Struct[struct {
			A string `json:"a"`
			B int    `json:"b"`
		}](Shape{"a": String(), "b": Int()}).SameAs("a", "b")
	})
	mustPanic(t, "RequiredIf on a required field", func() { base.RequiredIf("email", "kind", "company") })
	mustPanic(t, "RequiredIf with an unknown other", func() { base.RequiredIf("tax_id", "nope", "company") })
	mustPanic(
		t,
		"RequiredWhen on a required field",
		func() { base.RequiredWhen("email", func(signup) bool { return true }) },
	)
	mustPanic(t, "RequiredIfExists without others", func() { base.RequiredIfExists("tax_id") })
	mustPanic(t, "RequiredIfAnyMissing with an unknown other", func() { base.RequiredIfAnyMissing("tax_id", "nope") })
	mustPanic(t, "AfterField on non-time fields", func() { base.AfterField("phone", "fax") })

	// A rule is not shared with the schema it was added to.
	withRule := base.Confirmed("password")
	if _, err := base.Parse(okSignup()); err != nil {
		t.Errorf("the original schema got the rule: %v", err)
	}

	if _, err := withRule.Parse(okSignup()); err == nil {
		t.Error("the new schema lost the rule")
	}
}

func TestStructRulesNestedPath(t *testing.T) {
	t.Parallel()

	type billing struct {
		Kind  string  `json:"kind"`
		TaxID *string `json:"tax_id"`
	}

	type order struct {
		Billing billing `json:"billing"`
	}

	inner := Struct[billing](
		Shape{"kind": String(), "tax_id": String().Optional()},
	).RequiredIf("tax_id", "kind", "company")
	outer := Struct[order](Shape{"billing": inner})

	_, err := outer.Parse(map[string]any{"billing": map[string]any{"kind": "company"}})
	if diff := cmp.Diff([]issueRow{
		{"billing.tax_id", CodeRequired, "required_if", map[string]any{"other": "kind", "value": "company"}},
	}, issueRows(t, err)); diff != "" {
		t.Errorf("issues (-want +got):\n%s", diff)
	}
}

type booking struct {
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   time.Time  `json:"ends_at"`
	Until    *time.Time `json:"until"`
}

func bookingSchema() StructSchema[booking] {
	return Struct[booking](Shape{
		"starts_at": Time(), "ends_at": Time(), "until": Time().Optional(),
	})
}

func TestStructDateRules(t *testing.T) {
	t.Parallel()

	in := func(start, end string) map[string]any { return map[string]any{"starts_at": start, "ends_at": end} }

	tests := []struct {
		name   string
		schema StructSchema[booking]
		in     map[string]any
		want   []issueRow
	}{
		{"after passes", bookingSchema().AfterField("ends_at", "starts_at"), in("2026-01-01", "2026-01-02"), nil},
		{
			"after equal fails", bookingSchema().AfterField("ends_at", "starts_at"), in("2026-01-01", "2026-01-01"),
			[]issueRow{{"ends_at", CodeTooSmall, "date.after_field", map[string]any{"other": "starts at"}}},
		},
		{
			"after or equal passes",
			bookingSchema().AfterOrEqualField("ends_at", "starts_at"),
			in("2026-01-01", "2026-01-01"),
			nil,
		},
		{
			"after or equal fails",
			bookingSchema().AfterOrEqualField("ends_at", "starts_at"),
			in("2026-01-02", "2026-01-01"),
			[]issueRow{{"ends_at", CodeTooSmall, "date.after_or_equal_field", map[string]any{"other": "starts at"}}},
		},
		{"before passes", bookingSchema().BeforeField("starts_at", "ends_at"), in("2026-01-01", "2026-01-02"), nil},
		{
			"before fails", bookingSchema().BeforeField("starts_at", "ends_at"), in("2026-01-02", "2026-01-02"),
			[]issueRow{{"starts_at", CodeTooBig, "date.before_field", map[string]any{"other": "ends at"}}},
		},
		{
			"before or equal passes",
			bookingSchema().BeforeOrEqualField("starts_at", "ends_at"),
			in("2026-01-02", "2026-01-02"),
			nil,
		},
		{
			"before or equal fails",
			bookingSchema().BeforeOrEqualField("starts_at", "ends_at"),
			in("2026-01-03", "2026-01-02"),
			[]issueRow{{"starts_at", CodeTooBig, "date.before_or_equal_field", map[string]any{"other": "ends at"}}},
		},
		{
			"skipped when the other is absent",
			bookingSchema().AfterField("until", "starts_at"),
			in("2026-01-02", "2026-01-03"),
			nil,
		},
		{
			"pointer field", bookingSchema().AfterField("until", "starts_at"),
			with(in("2026-01-02", "2026-01-03"), "until", "2026-01-01"),
			[]issueRow{{"until", CodeTooSmall, "date.after_field", map[string]any{"other": "starts at"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.schema.Parse(tt.in)

			var got []issueRow
			if err != nil {
				got = issueRows(t, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("issues (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEmptyAsAbsent(t *testing.T) {
	t.Parallel()

	type profile struct {
		Nickname string  `json:"nickname"`
		Bio      string  `json:"bio"`
		Site     *string `json:"site"`
	}

	shape := Shape{
		"nickname": String().Optional().Default("anon"),
		"bio":      String().Min(1),
		"site":     String().Optional().URL(),
	}

	t.Run("off by default", func(t *testing.T) {
		t.Parallel()

		// Regression: "" stays a value, so Default is not used and URL runs.
		got, err := Struct[profile](shape).Parse(map[string]any{"nickname": "", "bio": "x", "site": ""})
		if got.Nickname != "" || err == nil {
			t.Errorf("Parse = %+v, %v; want the empty nickname kept and the empty URL refused", got, err)
		}
	})

	t.Run("on", func(t *testing.T) {
		t.Parallel()

		s := Struct[profile](shape).EmptyAsAbsent()

		got, err := s.Parse(map[string]any{"nickname": "", "bio": "x", "site": ""})
		if err != nil || got.Nickname != "anon" || got.Site != nil {
			t.Errorf("Parse = %+v, %v", got, err)
		}

		_, err = s.Parse(map[string]any{"bio": ""})
		if diff := cmp.Diff([]issueRow{{"bio", CodeRequired, CodeRequired, nil}}, issueRows(t, err)); diff != "" {
			t.Errorf("required empty (-want +got):\n%s", diff)
		}
	})
}

func TestBail(t *testing.T) {
	t.Parallel()

	type form struct {
		A string `json:"a"`
		B string `json:"b"`
		N int    `json:"n"`
	}

	shape := Shape{
		"a": String().Min(10).Email(),
		"b": String().Min(10).Email(),
		"n": Int().Min(10).MultipleOf(7),
	}

	_, err := Struct[form](shape).Parse(map[string]any{"a": "x", "b": "y", "n": 3})
	if got := len(issueRows(t, err)); got != 6 {
		t.Errorf("without Bail: %d issues, want 6 (every check of every field)", got)
	}

	_, err = Struct[form](shape).Bail().Parse(map[string]any{"a": "x", "b": "y", "n": 3})

	want := []issueRow{
		{"a", CodeTooSmall, "too_small.string", map[string]any{"min": 10}},
		{"b", CodeTooSmall, "too_small.string", map[string]any{"min": 10}},
		{"n", CodeTooSmall, "too_small.number", map[string]any{"min": 10}},
	}
	if diff := cmp.Diff(want, issueRows(t, err)); diff != "" {
		t.Errorf("with Bail (-want +got):\n%s", diff)
	}
}

func TestBailSkipsRulesOfFailedFields(t *testing.T) {
	t.Parallel()

	_, err := signupSchema().Bail().Confirmed("password").
		Parse(with(okSignup(), "password", "x", "password_confirmation", "y"))

	if diff := cmp.Diff(
		[]issueRow{{"password", CodeTooSmall, "too_small.string", map[string]any{"min": 8}}},
		issueRows(t, err),
	); diff != "" {
		t.Errorf("issues (-want +got):\n%s", diff)
	}
}

// Future and Past read the clock when parsing; synctest makes it fixed.
func TestTimeFuturePast(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		now := time.Now()

		if _, err := Time().Future().Parse(now.Add(time.Hour)); err != nil {
			t.Errorf("Future rejects tomorrow: %v", err)
		}

		_, err := Time().Future().Parse(now.Add(-time.Hour))
		if diff := cmp.Diff([]issueRow{{"", CodeTooSmall, "date.future", nil}}, issueRows(t, err)); diff != "" {
			t.Errorf("Future (-want +got):\n%s", diff)
		}

		if _, err := Time().Future().Parse(now); err == nil {
			t.Error("Future accepts the present instant")
		}

		if _, err := Time().Past().Parse(now.Add(-time.Hour)); err != nil {
			t.Errorf("Past rejects yesterday: %v", err)
		}

		_, err = Time().Past().Parse(now.Add(time.Hour))
		if diff := cmp.Diff([]issueRow{{"", CodeTooBig, "date.past", nil}}, issueRows(t, err)); diff != "" {
			t.Errorf("Past (-want +got):\n%s", diff)
		}

		// The zone of the input does not matter: instants are compared.
		zoned := now.Add(time.Hour).In(time.FixedZone("x", 14*3600)).Format(time.RFC3339)
		if _, err := Time().Future().Parse(zoned); err != nil {
			t.Errorf("Future rejects a future time in another zone: %v", err)
		}
	})
}

// Without the new options a schema reports what it always reported.
func TestStructOptionsAreOffByDefault(t *testing.T) {
	t.Parallel()

	type form struct {
		A string `json:"a"`
	}

	_, err := Struct[form](Shape{"a": String().Min(10).Email()}).Parse(map[string]any{"a": "x"})
	if got := len(issueRows(t, err)); got != 2 {
		t.Errorf("issues = %d, want both checks reported", got)
	}
}

func TestStrictAllowsTheConfirmationInput(t *testing.T) {
	t.Parallel()

	s := Struct[signup](Shape{
		"email": String(), "password": String(), "kind": String(),
		"tax_id": String().Optional(), "phone": String().Optional(), "fax": String().Optional(),
	}).
		Strict().Confirmed("password")

	in := with(okSignup(), "password_confirmation", "longenough")
	if _, err := s.Parse(in); err != nil {
		t.Errorf("Strict rejected the confirmation input: %v", err)
	}

	_, err := s.Parse(with(in, "extra", 1))
	if diff := cmp.Diff(
		[]issueRow{{"extra", CodeUnrecognizedKeys, CodeUnrecognizedKeys, nil}},
		issueRows(t, err),
	); diff != "" {
		t.Errorf("other unknown keys still fail (-want +got):\n%s", diff)
	}

	// Passthrough does not hand the confirmation over.
	var extra map[string]any

	pass := Struct[signup](Shape{"email": String(), "password": String(), "kind": String()}).
		Confirmed("password").Passthrough(func(_ *signup, e map[string]any) { extra = e })

	if _, err := pass.Parse(with(in, "other", 2)); err != nil {
		t.Fatal(err)
	}

	if _, ok := extra["password_confirmation"]; ok || extra["other"] != 2 {
		t.Errorf("extra = %v, want the unknown key and not the confirmation", extra)
	}
}

// A typed value has no raw input: the rules that ask what was sent are
// skipped, the comparison rules still apply.
func TestStructRulesOnTypedValues(t *testing.T) {
	t.Parallel()

	s := signupSchema().
		Confirmed("password").
		RequiredIf("tax_id", "kind", "company").
		RequiredIfAnyExists("fax", "phone")

	value := signup{Email: "a@b.co", Password: "longenough", Kind: "company"}

	if err := s.Check(value); err != nil {
		t.Errorf("Check skipped nothing: %v", err)
	}

	if _, err := s.Parse(value); err != nil {
		t.Errorf("Parse(T) skipped nothing: %v", err)
	}

	// A decoded request still gets every rule.
	if _, err := s.Parse(with(okSignup(), "kind", "company")); err == nil {
		t.Error("the request path lost RequiredIf")
	}

	same := signupSchema().SameAs("phone", "fax")

	one, two := "1", "2"
	if err := same.Check(
		signup{Email: "a@b.co", Password: "longenough", Kind: "person", Phone: &one, Fax: &two},
	); err == nil {
		t.Error("Check skipped a comparison rule")
	}

	if err := same.Check(signup{Email: "a@b.co", Password: "longenough", Kind: "person", Phone: &one}); err != nil {
		t.Errorf("a comparison with an unset field = %v, want skipped", err)
	}
}

func TestDerivedSchemasKeepTheRulesThatStillApply(t *testing.T) {
	t.Parallel()

	type update struct {
		Password *string `json:"password"`
		Kind     *string `json:"kind"`
		TaxID    *string `json:"tax_id"`
	}

	base := signupSchema().Strict().Confirmed("password").RequiredIf("tax_id", "kind", "company")

	// Partial keeps both: every key they cite is still there.
	partial := Partial[update](Pick[signup](base, "password", "kind", "tax_id"))

	_, err := partial.Parse(map[string]any{"password": "longenough", "password_confirmation": "nope"})
	if diff := cmp.Diff([]issueRow{{"password", CodeCustom, "confirmed", nil}}, issueRows(t, err)); diff != "" {
		t.Errorf("Partial lost Confirmed (-want +got):\n%s", diff)
	}

	if _, err := partial.Parse(
		map[string]any{"password": "longenough", "password_confirmation": "longenough"},
	); err != nil {
		t.Errorf("the derived schema rejects the confirmation input: %v", err)
	}

	if _, err := partial.Parse(map[string]any{"kind": "company"}); err == nil {
		t.Error("Partial lost RequiredIf")
	}

	// Omit of a cited key drops the rule that cites it, and keeps the other.
	omitted := Omit[update](signupSchema().Confirmed("password").RequiredIf("tax_id", "kind", "company"),
		"email", "phone", "fax", "kind")

	if _, err := omitted.Parse(
		map[string]any{"password": "longenough", "password_confirmation": "different"},
	); err == nil {
		t.Error("Omit dropped Confirmed, whose key was kept")
	}

	// kind is gone, so RequiredIf(tax_id, kind) is dropped: tax_id may be absent.
	if _, err := omitted.Parse(
		map[string]any{"password": "longenough", "password_confirmation": "longenough"},
	); err != nil {
		t.Errorf("Omit of kind should drop RequiredIf(tax_id, kind): %v", err)
	}

	noPassword := Omit[update](signupSchema().Confirmed("password"), "email", "password", "phone", "fax")
	if _, err := noPassword.Parse(
		map[string]any{"kind": "person", "password": "x", "password_confirmation": "y"},
	); err != nil {
		t.Errorf("Omit of the confirmed key kept the rule's check: %v", err)
	}
}

func TestRequiredIfReadsTheDefaultOfTheWatchedField(t *testing.T) {
	t.Parallel()

	s := Struct[signup](Shape{
		"email": String(), "password": String(),
		"kind":   String().Optional().Default("company"),
		"tax_id": String().Optional(),
	}).RequiredIf("tax_id", "kind", "company")

	_, err := s.Parse(map[string]any{"email": "a", "password": "p"})
	if diff := cmp.Diff([]issueRow{
		{"tax_id", CodeRequired, "required_if", map[string]any{"other": "kind", "value": "company"}},
	}, issueRows(t, err)); diff != "" {
		t.Errorf("issues (-want +got):\n%s", diff)
	}
}

func TestSameAsComparesInstantsOfTime(t *testing.T) {
	t.Parallel()

	s := Struct[booking](Shape{"starts_at": Time(), "ends_at": Time(), "until": Time().Optional()}).
		SameAs("starts_at", "ends_at")

	// The same instant written in two zones.
	if _, err := s.Parse(map[string]any{
		"starts_at": "2026-01-01T12:00:00Z", "ends_at": "2026-01-01T09:00:00-03:00",
	}); err != nil {
		t.Errorf("the same instant in two zones is different: %v", err)
	}
}

func TestTimeFutureAtPastAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	if _, err := Time().FutureAt(clock).Parse(now.Add(time.Second)); err != nil {
		t.Errorf("FutureAt rejects a later instant: %v", err)
	}

	if _, err := Time().FutureAt(clock).Parse(now); err == nil {
		t.Error("FutureAt accepts the instant of the clock")
	}

	if _, err := Time().PastAt(clock).Parse(now.Add(-time.Second)); err != nil {
		t.Errorf("PastAt rejects an earlier instant: %v", err)
	}

	if _, err := Time().PastAt(clock).Parse(now.Add(time.Second)); err == nil {
		t.Error("PastAt accepts a later instant")
	}

	mustPanic(t, "FutureAt(nil)", func() { Time().FutureAt(nil) })
	mustPanic(t, "PastAt(nil)", func() { Time().PastAt(nil) })
}
