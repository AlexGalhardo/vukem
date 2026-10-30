package notify_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/notify"
)

// TestSecurity_A01_NotifyScopedToUser: every read and write takes the user
// id, so one user can neither see nor mark another user's notifications,
// even with their ids.
func TestSecurity_A01_NotifyScopedToUser(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	nf, _, _ := newNotifier(t)

	victim, err := nf.Send(ctx, "victim", commentReplied{By: "secret"}, notify.Database)
	if err != nil {
		t.Fatal(err)
	}

	if page, _, err := nf.List(ctx, "attacker", "", 100); err != nil || len(page) != 0 {
		t.Errorf("attacker List = %+v, %v, want empty", page, err)
	}

	if n, _ := nf.Unread(ctx, "attacker"); n != 0 {
		t.Errorf("attacker Unread = %d, want 0", n)
	}

	if n, err := nf.MarkRead(ctx, "attacker", victim.ID); err != nil || n != 0 {
		t.Errorf("attacker MarkRead(victim id) = %d, %v, want 0", n, err)
	}

	if n, _ := nf.MarkAllRead(ctx, "attacker"); n != 0 {
		t.Errorf("attacker MarkAllRead = %d, want 0", n)
	}

	// A cursor taken from another user's page does not leak their rows.
	if page, _, _ := nf.List(ctx, "attacker", "~", 100); len(page) != 0 {
		t.Errorf("attacker List after a forged cursor = %+v", page)
	}

	if n, _ := nf.Unread(ctx, "victim"); n != 1 {
		t.Errorf("victim Unread = %d, want 1 (still unread)", n)
	}
}
