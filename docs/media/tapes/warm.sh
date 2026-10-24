#!/usr/bin/env bash
# Primes caches for the recordings; output is hidden by common.tape.
mkdir -p /tmp/w && cd /tmp/w || exit 1
vukem new shop --api </dev/null >/dev/null 2>&1
vukem new site --no-install </dev/null >/dev/null 2>&1
vukem new app --inertia react --no-install </dev/null >/dev/null 2>&1
(cd shop && vukem migrate >/dev/null 2>&1; go build ./... >/dev/null 2>&1)
(cd site && go build ./... >/dev/null 2>&1)
if [ -d /src ]; then
	cd /src && go test -count=1 -run XXX_NONE ./router/... ./web/... ./orm/... ./z/... ./auth/... ./cache/... ./csrf/... ./events/... ./queue/... ./ratelimit/... ./view/... >/dev/null 2>&1
fi
true
