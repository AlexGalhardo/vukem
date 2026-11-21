package z_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/z"
)

func TestCheckTags(t *testing.T) {
	t.Parallel()

	type good struct {
		N int `json:"n" validate:"min=1"`
	}

	type bad struct {
		N int `json:"n" validate:"gt=0"`
	}

	if err := z.CheckTags(reflect.TypeFor[good]()); err != nil {
		t.Fatalf("CheckTags(good) = %v, want nil", err)
	}

	if err := z.CheckTags(reflect.TypeFor[bad]()); err == nil || !strings.Contains(err.Error(), "unknown rule") {
		t.Fatalf("CheckTags(bad) = %v, want an unknown rule error", err)
	}
}
