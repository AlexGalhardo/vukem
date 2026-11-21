package vukem

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
)

func TestRebuildLinesIndexesFollowTheOptions(t *testing.T) {
	t.Parallel()

	tbl := schema.TableInfo{
		Name: "members",
		Columns: []schema.ColumnInfo{
			{Name: "id"}, {Name: "email"}, {Name: "nickname"},
		},
		Indexes: []schema.IndexInfo{
			{Name: "members_email_index", Columns: []string{"email"}},
			{Name: "by_nick", Columns: []string{"nickname"}, Unique: true},
			{Name: "members_lower", Expression: true},
			{Name: "members_email_key", Columns: []string{"email"}, Unique: true},
		},
	}

	plan := rebuildPlan{
		dropped: map[string]bool{"nickname": true},
		added:   map[string]bool{},
		renamed: map[string]string{},
	}
	lines, _, drop := rebuildLines(orm.ModelOf[indexedModel](), tbl, plan)

	if diff := cmp.Diff([]string{"by_nick", "members_lower", "members_email_key"}, drop); diff != "" {
		t.Errorf("dropped indexes (-want +got):\n%s", diff)
	}

	if !slices.Contains(lines, `t.Index("email")`) {
		t.Errorf("lines = %q, want the derived-name index kept as is", lines)
	}

	plan.dropped = map[string]bool{}
	lines, _, _ = rebuildLines(orm.ModelOf[indexedModel](), tbl, plan)

	if !slices.Contains(lines, `t.UniqueIndex("nickname").Name("by_nick")`) {
		t.Errorf("lines = %q, want the custom name kept with Name", lines)
	}

	if strings.Contains(strings.Join(lines, "\n"), "members_email_index") {
		t.Errorf("lines = %q, a derived name must not be written", lines)
	}
}
