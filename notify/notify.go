// Package notify stores in-app notifications per user and fans them out to
// mail and real-time broadcast.
//
// A notification is any value with a Kind; its JSON encoding is stored as
// the row's data. Send writes it through the channels you pass:
//
//	nf := notify.New(db, nil)
//	_, err := nf.Send(ctx, user.ID, CommentReplied{PostID: 7},
//		notify.Database,
//		notify.Mail(mailer, replyMail),
//		notify.Broadcast(hub, func(id string) string { return "private-user." + id }))
//
// SendTx does the same inside a transaction: the row is written with it,
// the mail is queued only if it commits and the broadcast is published after
// the commit. Unread, List, MarkRead and MarkAllRead serve the bell icon and
// the notification page; every query is scoped to the user id it receives.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/ulid"
)

// Table is the table holding notifications.
const Table = "notifications"

// MaxKind is the longest Kind a notification may have.
const MaxKind = 100

const (
	defaultLimit = 20
	maxLimit     = 100
)

// ErrNoChannels is returned by Send and SendTx without a channel.
var ErrNoChannels = errors.New("notify: no channel")

// Migration creates Table; register it with a timestamped id:
//
//	notify.Migration("20261029120000_create_notifications_table")
func Migration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			return s.Create(Table, func(t *schema.Table) {
				t.ULID("id").Primary()
				t.String("user_id", 191)
				t.String("kind", MaxKind)
				t.JSON("data")
				t.Timestamp("read_at").Nullable()
				t.Timestamp("created_at")
				t.Index("user_id", "id")
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists(Table) },
	}
}

// Notification is something a user is told about. Kind names it
// ("comment.replied") so the front end can pick an icon and a text; the
// JSON encoding of the value is stored as Stored.Data.
type Notification interface {
	Kind() string
}

// Stored is a notification as written to Table and returned by List. It is
// also the data a Broadcast channel publishes.
type Stored struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

// Channel delivers one notification. ex is the Executor Send or SendTx ran
// with (the transaction for SendTx); s is the stored form, n the original.
type Channel func(ctx context.Context, ex database.Executor, s Stored, n Notification) error

// Database is the channel that writes the notification to Table, inside the
// transaction when SendTx is used.
func Database(ctx context.Context, ex database.Executor, s Stored, _ Notification) error {
	_, err := ex.ExecContext(ctx, database.Rebind(ex.Dialect(),
		"INSERT INTO "+Table+" (id, user_id, kind, data, created_at) VALUES (?, ?, ?, ?, ?)"),
		s.ID, s.UserID, s.Kind, string(s.Data), s.CreatedAt)
	if err != nil {
		return fmt.Errorf("notify: insert: %w", err)
	}

	return nil
}

// Mail returns a channel that queues the mailable build returns with
// Mailer.SendLaterTx, so it is sent only if the transaction commits. build
// returning a nil mailable skips the mail (a user without an address, or one
// who turned these mails off). The Mailer needs a queue (mail.ErrNoQueue
// otherwise).
func Mail(
	m *mail.Mailer,
	build func(ctx context.Context, userID string, n Notification) (mail.Mailable, error),
) Channel {
	return func(ctx context.Context, ex database.Executor, s Stored, n Notification) error {
		mailable, err := build(ctx, s.UserID, n)
		if err != nil || mailable == nil {
			return err
		}

		return m.SendLaterTx(ctx, ex, mailable)
	}
}

// Publisher publishes an event on a channel; *broadcast.Server implements it.
type Publisher interface {
	Publish(ctx context.Context, channel, event string, data any) error
}

// Broadcast returns a channel that publishes the Stored notification as
// event Kind on the channel named by channelFor (a private per-user
// channel). Inside SendTx it publishes after the commit, and a publish
// error then cannot fail the committed work: it is dropped, since the
// Database row stays the source of truth the client reloads.
func Broadcast(p Publisher, channelFor func(userID string) string) Channel {
	return func(ctx context.Context, ex database.Executor, s Stored, _ Notification) error {
		publish := func(ctx context.Context) error { return p.Publish(ctx, channelFor(s.UserID), s.Kind, s) }

		if tx, ok := ex.(*database.Tx); ok {
			tx.AfterCommit(func(ctx context.Context) { _ = publish(ctx) })
			return nil
		}

		return publish(ctx)
	}
}

// Notifier sends and reads notifications in Table.
type Notifier struct {
	db  *database.DB
	ids *ulid.Generator
	now func() time.Time
}

// New returns a Notifier over db; now stamps created_at and read_at
// (time.Now when nil).
func New(db *database.DB, now func() time.Time) *Notifier {
	if now == nil {
		now = time.Now
	}

	return &Notifier{db: db, ids: ulid.NewGenerator(), now: now}
}

// Send delivers n to userID through each channel in order and returns the
// stored form. It stops at the first channel error.
func (nf *Notifier) Send(ctx context.Context, userID string, n Notification, channels ...Channel) (Stored, error) {
	return nf.SendTx(ctx, nf.db, userID, n, channels...)
}

// SendTx is Send inside the transaction of ex (a *database.Tx from DB.Tx):
// a rollback discards the row, the queued mail and the broadcast.
func (nf *Notifier) SendTx(
	ctx context.Context,
	ex database.Executor,
	userID string,
	n Notification,
	channels ...Channel,
) (Stored, error) {
	if len(channels) == 0 {
		return Stored{}, ErrNoChannels
	}

	kind := n.Kind()
	if userID == "" || kind == "" || len(kind) > MaxKind {
		return Stored{}, fmt.Errorf("notify: user id and a kind of 1 to %d bytes are required", MaxKind)
	}

	data, err := json.Marshal(n)
	if err != nil {
		return Stored{}, fmt.Errorf("notify: encode %s: %w", kind, err)
	}

	now := nf.now().UTC()
	s := Stored{ID: nf.ids.At(now), UserID: userID, Kind: kind, Data: data, CreatedAt: now}

	for _, ch := range channels {
		if err := ch(ctx, ex, s, n); err != nil {
			return s, err
		}
	}

	return s, nil
}

func (nf *Notifier) q(sqlText string) string { return database.Rebind(nf.db.Dialect(), sqlText) }

// Unread counts userID's unread notifications.
func (nf *Notifier) Unread(ctx context.Context, userID string) (int64, error) {
	var n int64

	err := nf.db.QueryRowContext(ctx,
		nf.q("SELECT COUNT(*) FROM "+Table+" WHERE user_id = ? AND read_at IS NULL"), userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("notify: unread: %w", err)
	}

	return n, nil
}

// List returns up to limit of userID's notifications, newest first, after
// the cursor (empty for the first page), and the cursor of the next page
// ("" on the last one). limit is clamped to 1..100; 0 means 20. The cursor
// is the last id of the page: ids sort by creation time.
func (nf *Notifier) List(ctx context.Context, userID, cursor string, limit int) ([]Stored, string, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	limit = min(limit, maxLimit)

	query := "SELECT id, user_id, kind, data, read_at, created_at FROM " + Table + " WHERE user_id = ?"
	args := []any{userID}

	if cursor != "" {
		query += " AND id < ?"
		args = append(args, cursor)
	}

	// One extra row tells whether a next page exists.
	rows, err := nf.db.QueryContext(ctx, nf.q(query+" ORDER BY id DESC LIMIT ?"), append(args, limit+1)...)
	if err != nil {
		return nil, "", fmt.Errorf("notify: list: %w", err)
	}
	defer rows.Close()

	var out []Stored

	for rows.Next() {
		var (
			s      Stored
			data   string
			readAt *time.Time
		)

		if err := rows.Scan(&s.ID, &s.UserID, &s.Kind, &data, &readAt, &s.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("notify: scan: %w", err)
		}

		s.Data, s.ReadAt = json.RawMessage(strings.Clone(data)), readAt
		out = append(out, s)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("notify: list: %w", err)
	}

	if len(out) <= limit {
		return out, "", nil
	}

	out = out[:limit]

	return out, out[limit-1].ID, nil
}

// MarkRead marks userID's notifications with the given ids as read and
// returns how many changed. Ids of other users' notifications are ignored.
func (nf *Notifier) MarkRead(ctx context.Context, userID string, ids ...string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	args := []any{nf.now().UTC(), userID}
	for _, id := range ids {
		args = append(args, id)
	}

	marks := strings.Repeat("?, ", len(ids)-1) + "?"

	return nf.exec(ctx, "mark read",
		"UPDATE "+Table+" SET read_at = ? WHERE user_id = ? AND read_at IS NULL AND id IN ("+marks+")", args...)
}

// MarkAllRead marks every unread notification of userID as read and returns
// how many changed.
func (nf *Notifier) MarkAllRead(ctx context.Context, userID string) (int64, error) {
	return nf.exec(ctx, "mark all read",
		"UPDATE "+Table+" SET read_at = ? WHERE user_id = ? AND read_at IS NULL", nf.now().UTC(), userID)
}

// Prune deletes the notifications, read or not, created before cutoff and
// returns how many it deleted. Schedule it to bound the table.
func (nf *Notifier) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	return nf.exec(ctx, "prune", "DELETE FROM "+Table+" WHERE created_at < ?", cutoff.UTC())
}

func (nf *Notifier) exec(ctx context.Context, op, query string, args ...any) (int64, error) {
	res, err := nf.db.ExecContext(ctx, nf.q(query), args...)
	if err != nil {
		return 0, fmt.Errorf("notify: %s: %w", op, err)
	}

	return res.RowsAffected()
}
