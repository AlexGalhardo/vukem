package redis

// SetBeforeRekey installs a hook that runs between claiming a legacy
// clear-id key and writing it under its hashed key.
func SetBeforeRekey(s *SessionStore, f func()) { s.beforeRekey = f }
