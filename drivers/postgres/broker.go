package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/AlexGalhardo/vukem/database"
)

// ------------------------------------------------------------ sse.Broker

// MaxNotifyPayload is the largest payload, in bytes, that Publish accepts.
//
// PostgreSQL rejects a notification payload of 8000 bytes or more, so this is
// the last size it takes. The server derives that bound from its block size,
// so a server built with a smaller BLCKSZ accepts less and answers with its
// own error; the default build accepts exactly this much. Events on an SSE
// topic are tens of bytes, so in practice the limit only catches a mistake
// such as streaming a whole document instead of a progress update.
const MaxNotifyPayload = 7999

// maxIdentifier is PostgreSQL's identifier limit (NAMEDATALEN - 1).
const maxIdentifier = 63

// notifyBuffer is how many notifications may wait for the subscriber before
// the next one is dropped. sse.Hub buffers again per stream; this buffer only
// absorbs a burst between the listening connection and the Hub.
const notifyBuffer = 64

// unlistenTimeout bounds the UNLISTEN that cleans a connection on its way
// back to the pool, which runs when the subscription's context is already
// done.
const unlistenTimeout = 5 * time.Second

// ErrPayloadTooLarge is returned by [Broker.Publish] for a payload over
// [MaxNotifyPayload]. An event that large does not belong on a notification
// channel, so it is refused rather than truncated: a truncated event would
// reach the browser as valid-looking, unparseable data.
var ErrPayloadTooLarge = errors.New("postgres: payload over the NOTIFY limit")

// ErrNotPostgres is returned by [NewBroker] when the database is not
// PostgreSQL, and by [Broker.Subscribe] when it is not reached through pgx.
// Both are configuration mistakes, and failing loudly beats a broker that
// accepts every publish and delivers nothing.
var ErrNotPostgres = errors.New("postgres: the broker needs a PostgreSQL connection through pgx")

// Broker carries server-sent events between processes over PostgreSQL
// LISTEN/NOTIFY. It implements sse.Broker, so an app whose web service and
// queue:work service are separate processes streams progress from the worker
// to the browser on the database it already has, without adding Redis purely
// as a message bus.
//
//	broker, err := postgres.NewBroker(db)
//	hub := sse.NewHub(broker)
//
// What it costs:
//
//   - A payload must stay at or under [MaxNotifyPayload] bytes (PostgreSQL
//     refuses a NOTIFY payload of 8000 bytes or more); Publish refuses a
//     larger one with [ErrPayloadTooLarge] rather than truncate it.
//   - LISTEN occupies a connection for the life of the subscription, so each
//     one is taken out of the pool (sse.Hub holds one per topic that has
//     local subscribers and releases it when the last one leaves, so the
//     count follows watched topics, not watching browsers). Size
//     database.Config.MaxOpen (10 by default) for the topics an instance
//     watches at once, plus the connections its requests need.
//   - A notification is not persisted and is not replayed: a subscriber that
//     is not connected when the event is published never sees it. The stream
//     is the fast path; durable state belongs in a table.
type Broker struct {
	db  *database.DB
	log *slog.Logger
}

// BrokerOption configures a [Broker].
type BrokerOption func(*Broker)

// WithBrokerLogger sets the logger a [Broker] reports a subscription that
// ended unexpectedly on; the default is slog.Default. A nil logger is
// ignored.
func WithBrokerLogger(log *slog.Logger) BrokerOption {
	return func(b *Broker) {
		if log != nil {
			b.log = log
		}
	}
}

// NewBroker returns a broker on db, which must be PostgreSQL. It fails rather
// than degrade on any other database: a broker that accepts every publish and
// delivers nothing looks like working code and breaks only in production,
// under load, in the one path nobody watches. Whether the connection really
// goes through pgx is only visible on a connection, so that half of
// [ErrNotPostgres] comes from [Broker.Subscribe].
func NewBroker(db *database.DB, opts ...BrokerOption) (*Broker, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: no database", ErrNotPostgres)
	}

	if name := db.Dialect().Name(); name != "postgres" {
		return nil, fmt.Errorf("%w: the dialect is %q", ErrNotPostgres, name)
	}

	b := &Broker{db: db, log: slog.Default()}

	for _, opt := range opts {
		opt(b)
	}

	return b, nil
}

// Publish sends payload to every listener of channel. A payload over
// [MaxNotifyPayload] bytes is refused with [ErrPayloadTooLarge].
func (b *Broker) Publish(ctx context.Context, channel string, payload []byte) error {
	if len(payload) > MaxNotifyPayload {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrPayloadTooLarge, len(payload), MaxNotifyPayload)
	}

	// pg_notify rather than the NOTIFY statement: NOTIFY takes a literal, not
	// a parameter, so it would mean concatenating the payload into SQL.
	// pg_notify is an ordinary function call with bound parameters.
	if _, err := b.db.ExecContext(ctx, "SELECT pg_notify($1, $2)",
		notifyIdentifier(channel), string(payload)); err != nil {
		return fmt.Errorf("postgres: notify %q: %w", channel, err)
	}

	return nil
}

// Subscribe delivers the payloads published on channels until ctx is done,
// then closes the returned channel. It returns once every LISTEN is in place,
// so an event published after it returns is delivered.
//
// The subscription holds one connection of the pool until ctx is done. A
// notification that the returned channel has no room for is dropped rather
// than allowed to block that connection, which serves every other subscriber
// of the same channels: sse.Hub already treats a gap as a reason for the
// client to reconnect, and a blocked listener would also stop the server from
// truncating its notification queue.
func (b *Broker) Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error) {
	if len(channels) == 0 {
		return nil, errors.New("postgres: subscribe needs at least one channel")
	}

	conn, err := b.db.SQL().Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: take a listening connection: %w", err)
	}

	// listening carries the outcome of the LISTENs, so Subscribe returns only
	// once the subscription is really in place.
	listening := make(chan error, 1)
	out := make(chan []byte, notifyBuffer)

	go func() {
		defer close(out)

		// The deferred calls run in reverse, so the connection is back in the
		// pool before out closes: a caller that waits for the close knows the
		// connection was released. Closing it here, rather than on the
		// caller's goroutine, is what makes "ctx is done" the single way a
		// subscription ends.
		defer func() {
			if cerr := conn.Close(); cerr != nil && !errors.Is(cerr, sql.ErrConnDone) {
				b.log.DebugContext(ctx, "postgres: closing the listening connection", "err", cerr)
			}
		}()

		err := conn.Raw(func(driverConn any) error {
			return b.pump(ctx, driverConn, channels, listening, out)
		})
		// Raw can fail before it ever calls the callback, and then nothing has
		// been sent on listening: Subscribe must never wait for a subscription
		// that will not happen. The send is non-blocking because in every
		// other case the outcome has already been sent, and a slot nobody
		// reads costs nothing. What breaks after the subscription is up has no
		// caller left to tell, so pump logs that itself.
		if err != nil {
			select {
			case listening <- err:
			default:
			}
		}
	}()

	if err := <-listening; err != nil {
		return nil, err
	}

	return out, nil
}

// pump issues the LISTENs and then forwards notifications until ctx is done.
// It runs inside sql.Conn.Raw, the only place the pgx connection may be
// touched, so the whole life of the subscription happens in here.
func (b *Broker) pump(
	ctx context.Context,
	driverConn any,
	channels []string,
	listening chan<- error,
	out chan<- []byte,
) (err error) {
	c, ok := driverConn.(*stdlib.Conn)
	if !ok {
		err = fmt.Errorf("%w: the driver connection is %T", ErrNotPostgres, driverConn)
		listening <- err

		return err
	}

	pc := c.Conn()

	// pgx's database/sql driver does not reset a session between borrowers, so
	// a connection would go back to the pool still registered: the next
	// borrower would keep the subscription alive, and a listening backend that
	// reads nothing stops the server from truncating its notification queue.
	// UNLISTEN * drops only the registrations below, since the subscription
	// owns this connection for its whole life. driver.ErrBadConn is how
	// sql.Conn.Raw is told to discard a connection it cannot clean.
	defer func() {
		if uerr := unlisten(ctx, pc); uerr != nil {
			b.log.DebugContext(ctx, "postgres: discarding the listening connection", "err", uerr)

			err = errors.Join(err, driver.ErrBadConn)
		}
	}()

	for _, channel := range channels {
		// LISTEN takes an identifier, not a parameter, and notifyIdentifier
		// produces [a-z0-9_] only, so the channel name cannot carry quoting
		// into the statement.
		if _, lerr := pc.Exec(ctx, "LISTEN "+notifyIdentifier(channel)); lerr != nil {
			err = fmt.Errorf("postgres: listen %q: %w", channel, lerr)
			listening <- err

			return err
		}
	}

	listening <- nil

	err = forward(ctx, pc, out)

	// A context that is done is how a subscription is meant to end. Anything
	// else ended one that was already up, and Subscribe has returned, so this
	// is the only place that failure is ever reported.
	if err != nil && ctx.Err() == nil {
		b.log.ErrorContext(ctx, "postgres: subscription ended", "channels", channels, "err", err)
	}

	return err
}

// forward moves notifications to out until ctx is done. A notification out
// has no room for is dropped on purpose: see [Broker.Subscribe].
func forward(ctx context.Context, pc *pgx.Conn, out chan<- []byte) error {
	for {
		n, err := pc.WaitForNotification(ctx)
		if err != nil {
			return err
		}

		select {
		case out <- []byte(n.Payload):
		default:
		}
	}
}

// unlisten ends the connection's registrations. It runs when ctx is already
// done, hence a context of its own, and reports nothing for a connection pgx
// has closed: there is no session left to clean, and the pool discards it.
func unlisten(ctx context.Context, pc *pgx.Conn) error {
	if pc.IsClosed() {
		return nil
	}

	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlistenTimeout)
	defer cancel()

	_, err := pc.Exec(uctx, "UNLISTEN *")

	return err
}

// notifyIdentifier maps a channel name to a PostgreSQL identifier.
//
// Channel names come from sse.Hub and look like "sse:conversions:<id>": they
// hold characters that would have to be quoted and they pass the 63-byte
// identifier limit easily. Hashing sidesteps both, and since LISTEN and
// pg_notify go through this one function they always agree. The result is
// lowercase, so the unquoted identifier LISTEN takes and the case-sensitive
// text pg_notify takes still name the same channel. The prefix keeps the
// channel recognisable in pg_stat_activity.
//
// The mapping is part of the wire format: every process of an app must derive
// it the same way, so it never changes.
func notifyIdentifier(channel string) string {
	sum := sha256.Sum256([]byte(channel))

	return "vukem_" + hex.EncodeToString(sum[:16])
}
