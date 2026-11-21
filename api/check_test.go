package api_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/web"
)

func TestCheckInputs(t *testing.T) {
	t.Parallel()

	type goodIn struct {
		Name  string `json:"name"  validate:"required,max=20"`
		Price int    `json:"price" validate:"min=0"`
	}

	type badRuleIn struct {
		Price int64 `json:"price" validate:"required,gt=0"`
	}

	type badTypeIn struct {
		Ch chan int `json:"ch"`
	}

	type noTagRawIn struct {
		Body api.RawBody
	}

	tests := []struct {
		name  string
		types []reflect.Type
		want  []string // substrings of the error; nil means no error
	}{
		{
			"valid and empty inputs",
			[]reflect.Type{reflect.TypeFor[goodIn](), reflect.TypeFor[struct{}](), reflect.TypeFor[importFile]()},
			nil,
		},
		{"unknown rule", []reflect.Type{reflect.TypeFor[badRuleIn]()}, []string{"badRuleIn", `unknown rule "gt=0"`}},
		{
			"unsupported field type",
			[]reflect.Type{reflect.TypeFor[badTypeIn]()},
			[]string{"badTypeIn", "unsupported type"},
		},
		{
			"raw body without json dash",
			[]reflect.Type{reflect.TypeFor[noTagRawIn]()},
			[]string{"noTagRawIn", "json:\"-\""},
		},
		{"not a struct", []reflect.Type{reflect.TypeFor[int]()}, []string{"must be a struct"}},
		{
			"every bad type is reported",
			[]reflect.Type{reflect.TypeFor[goodIn](), reflect.TypeFor[badRuleIn](), reflect.TypeFor[badTypeIn]()},
			[]string{"badRuleIn", "badTypeIn"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := api.CheckInputs(tt.types...)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("CheckInputs() = %v, want nil", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("CheckInputs() = nil, want an error containing %q", tt.want)
			}

			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("CheckInputs() = %v, want it to contain %q", err, w)
				}
			}
		})
	}
}

func TestRegisterStillPanicsOnAnUnknownRule(t *testing.T) {
	t.Parallel()

	type badIn struct {
		Price int64 `json:"price" validate:"gt=0"`
	}

	defer func() {
		if recover() == nil {
			t.Fatal("Register with an unknown rule did not panic")
		}
	}()

	api.Register(
		typedRouter(),
		"POST /bad",
		api.Op{},
		func(_ *web.Ctx, _ badIn) (struct{}, error) { return struct{}{}, nil },
	)
}
