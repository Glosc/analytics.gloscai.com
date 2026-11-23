# HTTP Compatibility Checks

`test_http_contract.py` exercises the backend through HTTP only and uses the Python standard library. It covers health, cookie-based auth, app ownership, stats authorization/query binding, ClickHouse persistence and stats filters/breakdowns, feature flags, error list/detail, CSV export contents, self-hosted billing, and shares/ownership-request lifecycle.

Write scenarios create accounts, apps, a flag, events, errors, shares, and app requests. They are skipped unless the target is loopback and `APTABASE_COMPAT_ALLOW_WRITES` is explicitly set to `I_HAVE_A_DISPOSABLE_TEST_DATABASE`. The harness does not clean up created data; run it only against a disposable service backed by a disposable database and discard that database afterward.

Run the full suite:

```sh
APTABASE_COMPAT_BASE_URL=http://127.0.0.1:27462 \
APTABASE_COMPAT_ALLOW_WRITES=I_HAVE_A_DISPOSABLE_TEST_DATABASE \
python3 -m unittest discover -s backend/compat -v
```

The reference C# service currently returns `500` when creating a feature flag: its insert omits the non-null `conditions` column. The test expects successful creation, so this known reference failure is intentional evidence for migration follow-up rather than a reason to weaken the assertion. The reference also reports one other share when the list is empty (the left join yields a null row); the test expects the useful count of zero.

The empty ownership-request query accepts either `200` with JSON `null` or `204` with an empty body; both local services have returned `204` after cancellation. Positive share/ownership acceptance needs an account with an email. The local username/password registration route creates email-less users, so this suite validates share grants/revocation and ownership-request create/duplicate/cancel/unauthorized-accept behavior without relying on OAuth or database access.
