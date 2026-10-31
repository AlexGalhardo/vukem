"""One client address per Schemathesis call.

A scan sends far more requests than a per-IP rate limit allows from one
address, and the missing-credentials check then sees 429 instead of 401. The
app must trust the Docker bridge (TRUSTED_PROXIES) so X-Forwarded-For is the
bucket key.
"""

import random

import schemathesis

_n = random.randrange(250 * 250)


def _fake_ip(n: int) -> str:
    return f"10.{(n >> 8) % 250}.{n % 250}.{1 + ((n * 7) % 250)}"


@schemathesis.hook
def before_call(context, case, kwargs) -> None:
    global _n
    _n += 1
    case.headers["X-Forwarded-For"] = _fake_ip(_n)
