package studio

import (
	"context"
	"slices"
)

// audit logs one write attempt on stderr, before the response. Values never
// appear: only the source, what was done, to what, and whether it worked.
func (s *Studio) audit(ctx context.Context, src Source, action, target string, err error, attrs ...any) {
	args := append(
		[]any{"source", src.Name(), "kind", string(src.Kind()), "action", action, "target", target},
		attrs...)

	args = append(args, "ok", err == nil)
	if err != nil {
		args = append(args, "error", errorClass(err))
	}

	s.cfg.Log.WarnContext(ctx, "studio write", args...)
}

// auditConsole records a MongoDB aggregate that was allowed to write.
func (s *Studio) auditConsole(ctx context.Context, m MongoSource, coll string, err error) {
	if s.cfg.Write {
		s.audit(ctx, m, "aggregate", coll, err)
	}
}

// auditRedisCommand records a console command: only its name, never its
// arguments, which hold keys and values. Read commands are not writes.
func (s *Studio) auditRedisCommand(ctx context.Context, src RedisSource, line string, err error) {
	args, perr := parseRedisLine(line)
	if perr != nil || !s.cfg.Write || !isRedisWrite(args[0]) {
		return
	}

	s.audit(ctx, src, "console", asciiUpper(args[0]), err, "args", len(args)-1)
}

func isRedisWrite(cmd string) bool {
	return slices.Contains(redisWriteCommands, asciiUpper(cmd))
}
