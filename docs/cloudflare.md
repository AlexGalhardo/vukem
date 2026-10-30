# Cloudflare (`contrib/cloudflare`)

`github.com/AlexGalhardo/vukem/contrib/cloudflare` is an optional, stdlib-only module for apps served behind Cloudflare. Every piece is opt-in and fails closed. Site page: `site/content/docs/deployment/cloudflare.mdx`.

Shipped:

- Real client IP: `cloudflare.Ranges()` returns Cloudflare's published IPv4 and IPv6 ranges, embedded from <https://www.cloudflare.com/ips-v4> and <https://www.cloudflare.com/ips-v6> (read 2026-10-29; reference: <https://developers.cloudflare.com/fundamentals/concepts/cloudflare-ip-addresses/>). Pass them to core with `vukem.WithTrustedProxies(cloudflare.Ranges()...)` and set `CLIENT_IP_HEADER=CF-Connecting-IP` (`cloudflare.ClientIPHeader`; header reference: <https://developers.cloudflare.com/fundamentals/reference/http-headers/>). `c.IP()`, `ratelimit.ByIP`, the request log and sessions then see the visitor's IP; a `CF-Connecting-IP` sent by any other peer is ignored.
- `vukem.WithTrustedProxies(prefixes ...netip.Prefix)` (core): proxies from code, added to `TRUSTED_PROXIES`; they satisfy the `CLIENT_IP_HEADER` and `TRUST_FORWARDED_PROTO` requirements. An invalid prefix is a boot error.
- `cloudflare.FetchRanges(ctx, client, urls...)` downloads the current lists (64 KiB cap; a list that does not parse, is empty or contains a `/0` is an error) so a scheduled job can warn when the embedded copy is stale. The ranges in a running app change with module upgrades, not at runtime.
- Origin lock: `cloudflare.OriginLock(allow ...netip.Prefix)` answers 403 to every request whose TCP peer is outside Cloudflare's ranges and `allow` (for example the network a platform health check comes from), so nobody bypasses Cloudflare's WAF and rate limits by calling the origin. It reads the peer address only, never a header. `cloudflare.FromCloudflare(r)` reports the same check.

- Visitor headers: `cloudflare.Headers()` middleware puts a `cloudflare.Visitor` in the request context (`cloudflare.VisitorFrom(ctx)`): `Country` (`CF-IPCountry`, ISO 3166-1 alpha-2, `XX` unknown, `T1` Tor), `Ray` (`CF-Ray`), `BotScore` (`cf-bot-score`, 1-99) and `VerifiedBot` (`cf-verified-bot`), the last two only with Bot Management's "Add bot protection headers" Managed Transform (<https://developers.cloudflare.com/rules/transform/managed-transforms/reference/>, read 2026-10-29). The headers are read only from a Cloudflare peer; from any other peer `VisitorFrom` reports false. Malformed values are dropped.

Limits: the checks need Cloudflare to be the TCP peer of the app. Behind another proxy (a platform load balancer between Cloudflare and the app) the peer is that proxy: trust it with `TRUSTED_PROXIES` instead, and lock the origin at that proxy or with Authenticated Origin Pulls.

Turnstile: use core `challenge.Turnstile` with `Hostname` and `Action` set. Planned in VF-052 (separate pull requests): CDN cache-control helpers and a purge client, Cloudflare Access JWT validation.

Tests: `TestSecurity_A05_OriginLockRefusesDirectRequests`, `TestSecurity_A01_VisitorHeadersOnlyFromCloudflare`, `TestVisitorFromCloudflare`, `TestSecurity_A01_TrustedProxiesFromOption` (core), `TestFetchRanges`, `FuzzParseRanges`.
