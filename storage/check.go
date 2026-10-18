package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AlexGalhardo/vukem/uuid"
)

// ErrUnhealthy reports that a disk did not complete a round trip. Every
// error [Check] returns wraps it, and none of them wraps the driver's own:
// an S3 error carries the endpoint, the bucket and sometimes the access
// key, and the error a health handler receives is the one a naive handler
// puts in the response body.
var ErrUnhealthy = errors.New("storage: disk is not healthy")

// checkPrefix holds every probe object. A disk shared with application
// files keeps them apart, and a lifecycle rule can expire anything a
// crashed process left behind.
const checkPrefix = ".vukem-health/"

// Check reports whether a disk can actually store a file: it writes a
// small object under a unique key, reads it back, compares the bytes and
// deletes it. Use it in a readiness check (App.AddHealthCheck), never in a
// liveness probe — it does real I/O against the remote.
//
// The round trip is the point. The obvious check is whether a known object
// exists, and that one passes when nothing works: given the wrong
// credentials several S3 implementations answer "object does not exist"
// rather than refusing, so an existence check reports a healthy disk while
// no upload can succeed. Only a write distinguishes the two, and reading
// the bytes back also catches a disk that is up and returning the wrong
// object.
//
// The delete runs even when ctx is already done, through
// [context.WithoutCancel], so a probe the client abandoned does not leave
// an object behind on every request. Errors wrap [ErrUnhealthy] and never
// the driver's error.
func Check(ctx context.Context, disk Disk) error {
	key := checkPrefix + uuid.NewV7().String()
	want := []byte("vukem health check\n")

	// Deleted before the error is returned, so a failing read or compare
	// does not leak the object either.
	defer func() {
		// WithoutCancel, not the request's ctx: a cancelled probe would
		// otherwise skip its own cleanup and leak one object per attempt.
		if err := disk.Delete(context.WithoutCancel(ctx), key); err != nil {
			_ = err // the write already failed, or the object was never there
		}
	}()

	if err := disk.Put(ctx, key, bytes.NewReader(want)); err != nil {
		return fmt.Errorf("%w: write refused", ErrUnhealthy)
	}

	r, err := disk.Open(ctx, key)
	if err != nil {
		return fmt.Errorf("%w: wrote an object it cannot read back", ErrUnhealthy)
	}

	got, readErr := io.ReadAll(r)

	if err := r.Close(); err != nil && readErr == nil {
		readErr = err
	}

	if readErr != nil {
		return fmt.Errorf("%w: read failed mid-object", ErrUnhealthy)
	}

	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: read back %d bytes, wrote %d", ErrUnhealthy, len(got), len(want))
	}

	// A disk that cannot delete still cannot be trusted with uploads, and
	// it fills up: the probe's own objects are the first evidence.
	if err := disk.Delete(ctx, key); err != nil {
		return fmt.Errorf("%w: delete refused", ErrUnhealthy)
	}

	return nil
}
