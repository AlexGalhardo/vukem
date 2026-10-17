package orm

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/i18n"
)

func TestValidateRules(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := seedUsers(ctx, t, db)

	if err := Validate(ctx, db,
		Unique[User]("email", "new@x.io"),
		Unique[User]("email", users[0].Email).Ignore(users[0].ID), // updating Ana keeps her email
		Exists[User]("id", users[1].ID),
		Exists[User]("id", nil), // empty: required-ness is the schema's job
		Unique[User]("email", ""),
	); err != nil {
		t.Fatalf("valid input = %v", err)
	}

	err := Validate(ctx, db,
		Unique[User]("email", users[1].Email),
		Unique[User]("email", users[0].Email).Ignore(users[1].ID).Field("contact").Message("Taken."),
		Exists[User]("id", int64(999)),
	)

	verr, ok := errors.AsType[*errs.ValidationError](err)
	if !ok || errs.Status(err) != 422 {
		t.Fatalf("err = %v", err)
	}

	if verr.First("email") != "The email has already been taken." || verr.First("contact") != "Taken." ||
		verr.First("id") != "The selected id is invalid." {
		t.Fatalf("fields = %v", verr.Fields)
	}

	// Soft-deleted rows still block Unique but fail Exists.
	p := Post{UserID: users[0].ID, Title: "gone"}
	if err := Create(ctx, db, &p); err != nil {
		t.Fatal(err)
	}

	if err := Delete(ctx, db, &p); err != nil {
		t.Fatal(err)
	}

	err = Validate(ctx, db, Unique[Post]("title", "gone"), Exists[Post]("id", p.ID))
	if verr, _ := errors.AsType[*errs.ValidationError](err); verr == nil || len(verr.Fields) != 2 {
		t.Fatalf("soft-deleted = %v", err)
	}

	if err := Validate(ctx, db, Exists[User]("nope", 1)); err == nil || errs.Status(err) == 422 {
		t.Fatalf("unknown column = %v", err)
	}

	email := "ana@x.io"
	if err := Validate(ctx, db, Unique[User]("email", &email)); err == nil {
		t.Fatal("pointer values are checked")
	}

	var none *string
	if err := Validate(ctx, db, Unique[User]("email", none)); err != nil {
		t.Fatal(err)
	}
}

// Unique and Exists messages follow the request locale: "validation.unique"
// and "validation.exists", with {field} from "validation.attributes.<field>".
func TestValidateRulesTranslated(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	users := seedUsers(ctx, t, db)

	b, err := i18n.New(i18n.Options{})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Add("pt-BR", map[string]any{"validation.attributes.email": "e-mail"}); err != nil {
		t.Fatal(err)
	}

	if err := b.AddBuiltin(); err != nil {
		t.Fatal(err)
	}

	err = Validate(errs.WithTranslator(ctx, b.Translator("pt-BR")),
		db,
		Unique[User]("email", users[1].Email),
		Unique[User]("email", users[0].Email).Field("contact").Message("Taken."),
		Exists[User]("id", int64(999)),
	)

	verr, ok := errors.AsType[*errs.ValidationError](err)
	if !ok {
		t.Fatalf("err = %v", err)
	}

	want := map[string][]string{
		"email":   {"O campo e-mail já está em uso."},
		"contact": {"Taken."},
		"id":      {"O campo id selecionado é inválido."},
	}
	if diff := cmp.Diff(want, verr.Fields); diff != "" {
		t.Errorf("fields (-want +got):\n%s", diff)
	}
}
