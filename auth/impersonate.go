package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// ErrImpersonation is returned, as a 409, by Impersonate when the session
// already impersonates someone or the target is the signed-in user, and by
// StopImpersonating when the session impersonates no one.
var ErrImpersonation = errors.New("auth: impersonation not allowed here")

func (g *SessionGuard[U]) impersonatorKey() string { return "_auth_impersonator_" + g.name }

func (g *SessionGuard[U]) impersonatorVersionKey() string {
	return "_auth_impersonator_version_" + g.name
}

func (g *SessionGuard[U]) impersonateUntilKey() string { return "_auth_impersonate_until_" + g.name }

// Impersonate signs the current user (the actor, typically an admin) in as
// target for at most ttl, to reproduce what target sees. Authorizing the
// actor is the caller's job: run it behind an admin-only gate. The session
// id is regenerated, no remember-me token is issued, and a security event
// "auth.impersonation_started" records actor and target. Nested
// impersonation and impersonating oneself are refused with ErrImpersonation.
// Once ttl has passed the whole session ends on the next request. Show a
// banner while [SessionGuard.Impersonator] reports true, and call
// [SessionGuard.StopImpersonating] to switch back.
func (g *SessionGuard[U]) Impersonate(c *web.Ctx, target U, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("auth: Impersonate needs a positive ttl")
	}

	actor, err := g.User(c)
	if err != nil {
		return err
	}

	if _, ok := g.Impersonator(c); ok || actor.AuthID() == target.AuthID() {
		return errs.Wrap(http.StatusConflict, ErrImpersonation)
	}

	s := c.Session()
	actorVersion := s.Get(versionKey(g.name))

	if err := g.Login(c, target); err != nil {
		return err
	}

	s.Put(g.impersonatorKey(), actor.AuthID())
	s.Put(g.impersonateUntilKey(), time.Now().Add(ttl).UTC().Format(time.RFC3339))

	if actorVersion != nil {
		s.Put(g.impersonatorVersionKey(), actorVersion)
	}

	web.SecurityEvent(c.Request(), "auth.impersonation_started",
		"guard", g.name, "actor", actor.AuthID(), "user", target.AuthID())

	return nil
}

// Impersonator returns the id of the user who started the current
// impersonation, and whether the session impersonates someone.
func (g *SessionGuard[U]) Impersonator(c *web.Ctx) (string, bool) {
	s := c.Session()
	if s == nil {
		return "", false
	}

	id, ok := s.Get(g.impersonatorKey()).(string)

	return id, ok && id != ""
}

// StopImpersonating signs the actor back in and returns it. When the actor
// no longer exists or its sessions were ended meanwhile (a password reset
// bumped its session version), the whole session ends instead and the
// error is ErrUnauthenticated.
func (g *SessionGuard[U]) StopImpersonating(c *web.Ctx) (U, error) {
	var zero U

	actorID, ok := g.Impersonator(c)
	if !ok {
		return zero, errs.Wrap(http.StatusConflict, ErrImpersonation)
	}

	s := c.Session()
	userID, _ := s.Get(g.key()).(string)

	actor, err := g.provider.ByID(c.Context(), actorID)
	if err != nil && !isNotFound(err) {
		return zero, errs.Wrap(http.StatusServiceUnavailable, err)
	}

	if saved := s.Get(g.impersonatorVersionKey()); saved != nil {
		s.Put(versionKey(g.name), saved)
	} else {
		s.Forget(versionKey(g.name))
	}

	if err != nil || !g.sessionVersionOK(s, actor) {
		s.Invalidate()
		cache(c, g.name, nil)
		web.SecurityEvent(c.Request(), "auth.impersonation_ended", "guard", g.name, "actor", actorID, "user", userID,
			"reason", "actor signed out")

		return zero, ErrUnauthenticated
	}

	if err := g.Login(c, actor); err != nil {
		return zero, err
	}

	g.forgetImpersonation(s)
	web.SecurityEvent(c.Request(), "auth.impersonation_ended", "guard", g.name, "actor", actorID, "user", userID)

	return actor, nil
}

func (g *SessionGuard[U]) forgetImpersonation(s web.Session) {
	s.Forget(g.impersonatorKey())
	s.Forget(g.impersonatorVersionKey())
	s.Forget(g.impersonateUntilKey())
}

// impersonationExpired reports a session impersonating someone past its
// deadline; a missing or unreadable deadline counts as expired.
func (g *SessionGuard[U]) impersonationExpired(s web.Session) bool {
	if s.Get(g.impersonatorKey()) == nil {
		return false
	}

	raw, _ := s.Get(g.impersonateUntilKey()).(string)

	until, err := time.Parse(time.RFC3339, raw)

	return err != nil || !time.Now().Before(until)
}
