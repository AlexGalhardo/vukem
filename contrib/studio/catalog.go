package studio

import (
	"context"
	"regexp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// schemaCatalog is the Introspector over schema.Inspect: the only code of the
// module that reads the schema package, so a change to its types lands here.
// Only names, types, defaults, primary keys and foreign keys are copied.
type schemaCatalog struct{ ex database.Executor }

// Tables implements Introspector.
func (c schemaCatalog) Tables(ctx context.Context) ([]Table, error) {
	snap, err := schema.Inspect(ctx, c.ex)
	if err != nil {
		return nil, err
	}

	out := make([]Table, len(snap.Tables))
	for i, t := range snap.Tables {
		out[i] = tableOf(t)
	}

	return out, nil
}

// identRe is what schema.InspectTable accepts as a table name; anything else
// is refused there with an error, which for a browser means "no such table".
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Table implements Introspector.
func (c schemaCatalog) Table(ctx context.Context, name string) (Table, bool, error) {
	if !identRe.MatchString(name) {
		return Table{}, false, nil
	}

	t, ok, err := schema.InspectTable(ctx, c.ex, name)
	if err != nil || !ok {
		return Table{}, false, err
	}

	return tableOf(t), true, nil
}

func tableOf(t schema.TableInfo) Table {
	out := Table{Name: t.Name, PrimaryKey: t.PrimaryKey}

	for _, c := range t.Columns {
		col := Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable}
		if c.Default.Valid {
			col.Default = c.Default.V
		}

		out.Columns = append(out.Columns, col)
	}

	for _, fk := range t.ForeignKeys {
		out.ForeignKeys = append(
			out.ForeignKeys,
			ForeignKey{Columns: fk.Columns, RefTable: fk.RefTable, RefColumns: fk.RefColumns},
		)
	}

	return out
}
