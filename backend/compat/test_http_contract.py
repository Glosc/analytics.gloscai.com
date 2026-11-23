"""Black-box HTTP contract checks for the Go backend migration.

This intentionally uses only the Python standard library. Write scenarios are
disabled unless the caller explicitly confirms a disposable local test DB.
"""

from __future__ import annotations

import http.cookiejar
import ipaddress
import json
import os
import time
import unittest
import uuid
import csv
import io
from datetime import datetime, timedelta, timezone
from urllib.error import HTTPError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, HTTPCookieProcessor, Request, build_opener


BASE_URL_ENV = "APTABASE_COMPAT_BASE_URL"
WRITE_GUARD_ENV = "APTABASE_COMPAT_ALLOW_WRITES"
WRITE_GUARD_VALUE = "I_HAVE_A_DISPOSABLE_TEST_DATABASE"
DEFAULT_PASSWORD = "CompatTest123!"


def configured_base_url() -> str:
    raw = os.environ.get(BASE_URL_ENV, "").strip()
    if not raw:
        raise unittest.SkipTest(f"Set {BASE_URL_ENV} to the local Go service URL")

    parsed = urlsplit(raw)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise unittest.SkipTest(f"{BASE_URL_ENV} must be an absolute HTTP(S) URL")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise unittest.SkipTest(f"{BASE_URL_ENV} must not contain credentials, query, or fragment")

    try:
        is_loopback = ipaddress.ip_address(parsed.hostname).is_loopback
    except ValueError:
        is_loopback = parsed.hostname.lower() == "localhost"
    if not is_loopback:
        raise unittest.SkipTest("Compatibility tests only target a local service on loopback")

    return raw.rstrip("/")


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class HttpApi:
    def __init__(self, base_url: str):
        self.base_url = base_url
        self.cookies = http.cookiejar.CookieJar()
        self.opener = build_opener(NoRedirect(), HTTPCookieProcessor(self.cookies))

    def request(self, method: str, path: str, body=None, headers=None, raw_body: str | None = None):
        data = raw_body.encode("utf-8") if raw_body is not None else (
            None if body is None else json.dumps(body, separators=(",", ":")).encode("utf-8")
        )
        request_headers = {"Accept": "application/json"}
        if data is not None:
            request_headers["Content-Type"] = "application/json"
        if headers:
            request_headers.update(headers)

        request = Request(self.base_url + path, data=data, headers=request_headers, method=method)
        try:
            response = self.opener.open(request, timeout=15)
        except HTTPError as error:
            response = error

        with response:
            raw = response.read()
            text = raw.decode("utf-8", errors="replace")
            self.last_headers = dict(response.headers.items())
            try:
                payload = json.loads(text) if text else None
            except json.JSONDecodeError:
                payload = None
            return response.status, payload, text


class ReadOnlyContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base_url = configured_base_url()
        cls.api = HttpApi(cls.base_url)

    def test_health_check(self):
        status, _, _ = self.api.request("GET", "/healthz")
        self.assertEqual(status, 200)

    def test_account_and_app_routes_require_a_session(self):
        for path in ("/api/_auth/me", "/api/_apps"):
            with self.subTest(path=path):
                status, _, _ = self.api.request("GET", path)
                self.assertEqual(status, 401)


class WritableContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base_url = configured_base_url()
        if os.environ.get(WRITE_GUARD_ENV) != WRITE_GUARD_VALUE:
            raise unittest.SkipTest(
                f"Writes disabled; set {WRITE_GUARD_ENV}={WRITE_GUARD_VALUE} only for a disposable test DB"
            )

        cls.user_a = cls.register_user("Owner A")
        cls.user_b = cls.register_user("Owner B")
        cls.app_a = cls.create_app(cls.user_a, "compat-a")
        cls.app_b = cls.create_app(cls.user_b, "compat-b")
        cls.stats_app = cls.create_app(cls.user_a, "compat-stats")
        cls.stats_sample = None

    @classmethod
    def register_user(cls, name: str):
        api = HttpApi(cls.base_url)
        username = f"compat_{uuid.uuid4().hex[:16]}"
        status, _, body = api.request(
            "POST",
            "/api/_auth/register",
            {"name": name, "username": username, "password": DEFAULT_PASSWORD},
        )
        if status != 200:
            raise AssertionError(f"register returned {status}: {body}")

        status, user, body = api.request("GET", "/api/_auth/me")
        if status != 200 or not isinstance(user, dict):
            raise AssertionError(f"registration did not establish a cookie session ({status}): {body}")
        if user.get("username") != username:
            raise AssertionError(f"/api/_auth/me returned unexpected identity: {body}")
        return {"api": api, "username": username, "password": DEFAULT_PASSWORD, "user": user}

    @staticmethod
    def create_app(account, name: str):
        status, app, body = account["api"].request("POST", "/api/_apps", {"name": f"{name}-{uuid.uuid4().hex[:8]}"})
        if status != 200 or not isinstance(app, dict) or not app.get("id") or not app.get("appKey"):
            raise AssertionError(f"app creation returned {status} or omitted id/appKey: {body}")
        return app

    @classmethod
    def create_stats_sample(cls):
        if cls.stats_sample is not None:
            return cls.stats_sample

        token = uuid.uuid4().hex[:10]
        event_name = f"CompatEvent{token}"
        other_event_name = f"CompatOther{token}"
        session_id = str(uuid.uuid4())
        base_time = datetime.now(timezone.utc).replace(microsecond=0)
        os_name = f"CompatOS{token[:6]}"
        os_version = f"v{token[:8]}"
        device_model = f"CompatDevice{token[:6]}"
        app_version = f"compat{token[:8]}"
        build_number = f"b{token[:8]}"
        events = []
        for index, amount in enumerate((2, 4, 6)):
            events.append(
                {
                    "eventName": event_name,
                    "timestamp": (base_time + timedelta(seconds=index)).isoformat(),
                    "sessionId": session_id,
                    "systemProps": {
                        "isDebug": False,
                        "osName": os_name,
                        "osVersion": os_version,
                        "deviceModel": device_model,
                        "appVersion": app_version,
                        "appBuildNumber": build_number,
                        "sdkVersion": "compat-http-test",
                    },
                    "props": {"campaign": token, "variant": "blue", "amount": amount},
                }
            )
        events.append(
            {
                "eventName": other_event_name,
                "timestamp": (base_time + timedelta(seconds=3)).isoformat(),
                "sessionId": session_id,
                "systemProps": {
                    "isDebug": False,
                    "osName": os_name,
                    "osVersion": os_version,
                    "deviceModel": device_model,
                    "appVersion": app_version,
                    "appBuildNumber": build_number,
                    "sdkVersion": "compat-http-test",
                },
                "props": {"campaign": token, "variant": "control", "amount": 99},
            }
        )
        headers = {"App-Key": cls.stats_app["appKey"], "CloudFront-Viewer-Address": "8.8.8.8"}
        status, _, body = cls.user_a["api"].request("POST", "/api/v0/events", events, headers)
        if status != 200:
            raise AssertionError(f"event batch returned {status}: {body}")

        cls.stats_sample = {
            "app": cls.stats_app,
            "event_name": event_name,
            "other_event_name": other_event_name,
            "session_id": session_id,
            "token": token,
            "os_name": os_name,
            "os_version": os_version,
            "device_model": device_model,
            "app_version": app_version,
            "build_number": build_number,
            "start": (base_time - timedelta(minutes=10)).isoformat(),
            "end": (base_time + timedelta(hours=1)).isoformat(),
        }
        return cls.stats_sample

    @staticmethod
    def sample_query(sample, **filters):
        params = {
            "buildMode": "release",
            "appId": sample["app"]["id"],
            "startDate": sample["start"],
            "endDate": sample["end"],
            "granularity": "Hour",
        }
        params.update(filters)
        return urlencode(params)

    def wait_for_event_count(self, sample, expected, filters=None):
        deadline = time.monotonic() + 45
        last_metrics = None
        while time.monotonic() < deadline:
            query = self.sample_query(sample, **(filters or {}))
            status, last_metrics, body = self.user_a["api"].request("GET", f"/api/_stats/metrics?{query}")
            self.assertEqual(status, 200, body)
            current = last_metrics.get("current", {}) if isinstance(last_metrics, dict) else {}
            if current.get("events", 0) >= expected:
                return last_metrics
            time.sleep(2)
        self.fail(f"ClickHouse did not expose {expected} events after ingestion; last metrics: {last_metrics}")

    def test_registration_signs_in_and_duplicate_username_conflicts(self):
        status, user, body = self.user_a["api"].request("GET", "/api/_auth/me")
        self.assertEqual(status, 200, body)
        self.assertEqual(user.get("username"), self.user_a["username"])
        self.assertIn(user.get("role"), ("admin", "user"))

        duplicate = HttpApi(self.base_url)
        status, _, _ = duplicate.request(
            "POST",
            "/api/_auth/register",
            {"name": "Duplicate", "username": self.user_a["username"], "password": DEFAULT_PASSWORD},
        )
        self.assertEqual(status, 409)

    def test_login_password_contract(self):
        client = HttpApi(self.base_url)
        status, _, _ = client.request(
            "POST", "/api/_auth/login", {"username": self.user_a["username"], "password": "WrongCompat123!"}
        )
        self.assertEqual(status, 401)

        status, _, body = client.request(
            "POST",
            "/api/_auth/login",
            {"username": self.user_a["username"], "password": self.user_a["password"]},
        )
        self.assertEqual(status, 200, body)
        status, me, body = client.request("GET", "/api/_auth/me")
        self.assertEqual(status, 200, body)
        self.assertEqual(me.get("username"), self.user_a["username"])

    def test_apps_are_listed_only_for_owner(self):
        status, apps, body = self.user_a["api"].request("GET", "/api/_apps")
        self.assertEqual(status, 200, body)
        self.assertIn(self.app_a["id"], {app.get("id") for app in apps})
        self.assertNotIn(self.app_b["id"], {app.get("id") for app in apps})

        status, own_app, body = self.user_a["api"].request("GET", f"/api/_apps/{self.app_a['id']}")
        self.assertEqual(status, 200, body)
        self.assertEqual(own_app.get("id"), self.app_a["id"])

    def test_stats_allow_owner_and_forbid_cross_user_app_ids(self):
        query = urlencode(
            {
                "buildMode": "release",
                "appId": self.app_a["id"],
                "startDate": "2026-01-01",
                "endDate": "2026-02-01",
            }
        )
        status, metrics, body = self.user_a["api"].request("GET", f"/api/_stats/metrics?{query}")
        self.assertEqual(status, 200, body)
        self.assertIsInstance(metrics, dict)
        self.assertIn("current", metrics)

        endpoints = (
            "/api/_stats/top-countries",
            "/api/_stats/top-osversions",
            "/api/_stats/top-devices",
            "/api/_stats/top-operatingsystems",
            "/api/_stats/top-regions",
            "/api/_stats/top-events",
            "/api/_stats/top-appversions",
            "/api/_stats/top-appbuildnumbers",
            "/api/_stats/metrics",
            "/api/_stats/periodic",
            "/api/_stats/top-props",
            "/api/_stats/live-geo",
            "/api/_stats/live-sessions",
            "/api/_stats/live-session-details",
            "/api/_stats/historical-sessions",
        )
        params = urlencode(
            {
                "AppId": self.app_a["id"],
                "body.AppId": self.app_b["id"],
                "body.BuildMode": "release",
                "body.StartDate": "2026-01-01",
                "body.EndDate": "2026-02-01",
                "body.SessionId": str(uuid.uuid4()),
            }
        )
        for endpoint in endpoints:
            with self.subTest(endpoint=endpoint):
                status, _, _ = self.user_a["api"].request("GET", f"{endpoint}?{params}")
                self.assertEqual(status, 403)

    def test_stats_persisted_breakdowns_and_filters(self):
        sample = self.create_stats_sample()
        filters = {
            "eventName": sample["event_name"],
            "appVersion": sample["app_version"],
            "osName": sample["os_name"],
            "deviceModel": sample["device_model"],
        }
        metrics = self.wait_for_event_count(sample, 3, filters)
        self.assertEqual(metrics["current"].get("sessions"), 1)

        no_match_query = self.sample_query(sample, eventName=f"missing-{sample['token']}")
        status, empty_metrics, body = self.user_a["api"].request("GET", f"/api/_stats/metrics?{no_match_query}")
        self.assertEqual(status, 200, body)
        self.assertEqual(empty_metrics.get("current", {}).get("events"), 0)
        wrong_version_query = self.sample_query(
            sample, eventName=sample["event_name"], appVersion=f"missing-{sample['token']}"
        )
        status, empty_metrics, body = self.user_a["api"].request(
            "GET", f"/api/_stats/metrics?{wrong_version_query}"
        )
        self.assertEqual(status, 200, body)
        self.assertEqual(empty_metrics.get("current", {}).get("events"), 0)

        endpoint_expectations = {
            "/api/_stats/top-countries": None,
            "/api/_stats/top-osversions": sample["os_version"],
            "/api/_stats/top-devices": sample["device_model"],
            "/api/_stats/top-operatingsystems": sample["os_name"],
            "/api/_stats/top-regions": None,
            "/api/_stats/top-events": sample["event_name"],
            "/api/_stats/top-appversions": sample["app_version"],
            "/api/_stats/top-appbuildnumbers": sample["build_number"],
        }
        for endpoint, expected_name in endpoint_expectations.items():
            with self.subTest(endpoint=endpoint):
                query = self.sample_query(sample, **filters)
                status, rows, body = self.user_a["api"].request("GET", f"{endpoint}?{query}")
                self.assertEqual(status, 200, body)
                self.assertIsInstance(rows, list)
                self.assertTrue(rows, f"No breakdown rows for {endpoint}")
                if expected_name is not None:
                    matching = [row for row in rows if row.get("name") == expected_name]
                    self.assertTrue(matching, f"Missing {expected_name!r} in {rows}")
                    expected_value = 3 if endpoint.endswith("top-events") else 1
                    self.assertEqual(matching[0].get("value"), expected_value)
                else:
                    self.assertTrue(any(row.get("value") == 1 for row in rows), rows)

        periodic_query = self.sample_query(sample, **filters)
        status, periodic, body = self.user_a["api"].request("GET", f"/api/_stats/periodic?{periodic_query}")
        self.assertEqual(status, 200, body)
        self.assertIsInstance(periodic, list)
        self.assertEqual(sum(row.get("events", 0) for row in periodic), 3)
        self.assertEqual(sum(row.get("sessions", 0) for row in periodic), 1)

        props_query = self.sample_query(sample, **filters)
        status, props, body = self.user_a["api"].request("GET", f"/api/_stats/top-props?{props_query}")
        self.assertEqual(status, 200, body)
        campaign_rows = [
            row for row in props
            if row.get("stringKey") == "campaign" and row.get("stringValue") == sample["token"]
        ]
        amount_rows = [
            row for row in props
            if row.get("numericKey") == "amount" and row.get("stringKey") == "campaign"
            and row.get("stringValue") == sample["token"]
        ]
        self.assertTrue(campaign_rows, f"Missing string property breakdown: {props}")
        self.assertTrue(amount_rows, f"Missing numeric property breakdown: {props}")
        self.assertEqual(amount_rows[0].get("events"), 3)
        self.assertAlmostEqual(amount_rows[0].get("median", 0), 4)
        self.assertAlmostEqual(amount_rows[0].get("min", 0), 2)
        self.assertAlmostEqual(amount_rows[0].get("max", 0), 6)
        self.assertAlmostEqual(amount_rows[0].get("sum", 0), 12)

        live_query = urlencode({"buildMode": "release", "appId": sample["app"]["id"]})
        status, geo, body = self.user_a["api"].request("GET", f"/api/_stats/live-geo?{live_query}")
        self.assertEqual(status, 200, body)
        self.assertIsInstance(geo, list)
        self.assertTrue(geo, "No persisted geo breakdown for the known public test IP")
        if geo:
            self.assertGreaterEqual(geo[0].get("users", 0), 1)
            for field in ("countryCode", "regionName", "latitude", "longitude"):
                self.assertIn(field, geo[0])

        status, sessions, body = self.user_a["api"].request("GET", f"/api/_stats/live-sessions?{live_query}")
        self.assertEqual(status, 200, body)
        session = next((row for row in sessions if row.get("id") == sample["session_id"]), None)
        self.assertIsNotNone(session, f"Persisted session missing from live view: {sessions}")
        self.assertEqual(session.get("eventsCount"), 4)

        detail_query = urlencode({**{"buildMode": "release", "appId": sample["app"]["id"]}, "sessionId": sample["session_id"]})
        status, detail, body = self.user_a["api"].request(
            "GET", f"/api/_stats/live-session-details?{detail_query}"
        )
        self.assertEqual(status, 200, body)
        self.assertEqual(detail.get("id"), sample["session_id"])
        self.assertEqual(detail.get("eventsCount"), 4)
        self.assertIn(sample["event_name"], detail.get("eventsName", []))

        historical_query = urlencode(
            {
                "buildMode": "release",
                "appId": sample["app"]["id"],
                "eventName": sample["event_name"],
                "appVersion": sample["app_version"],
            }
        )
        status, history, body = self.user_a["api"].request(
            "GET", f"/api/_stats/historical-sessions?{historical_query}"
        )
        self.assertEqual(status, 200, body)
        self.assertTrue(any(row.get("id") == sample["session_id"] for row in history), history)

    def test_export_returns_persisted_csv_rows(self):
        sample = self.create_stats_sample()
        self.wait_for_event_count(sample, 3, {"eventName": sample["event_name"]})
        query = urlencode(
            {
                "buildMode": "release",
                "appId": sample["app"]["id"],
                "appName": "Compat Export",
                "format": "csv",
                "startDate": sample["start"],
                "endDate": sample["end"],
            }
        )
        status, _, content = self.user_a["api"].request("GET", f"/api/_export/download?{query}")
        self.assertEqual(status, 200, content[:500])
        content_type = next(
            (value for key, value in self.user_a["api"].last_headers.items() if key.lower() == "content-type"), ""
        )
        self.assertIn("text/csv", content_type.lower())

        rows = list(csv.DictReader(io.StringIO(content)))
        self.assertTrue(rows, "Export returned headers but no persisted event rows")
        self.assertEqual(len(rows), 4)
        self.assertEqual({row.get("event_name") for row in rows}, {sample["event_name"], sample["other_event_name"]})
        self.assertTrue(all(sample["token"] in row.get("string_props", "") for row in rows))
        self.assertTrue(any('"amount":2' in row.get("numeric_props", "") for row in rows))

        forbidden_query = urlencode(
            {
                "buildMode": "release",
                "appId": self.app_b["id"],
                "appName": "Other App",
                "format": "csv",
                "startDate": sample["start"],
                "endDate": sample["end"],
            }
        )
        status, _, _ = self.user_a["api"].request("GET", f"/api/_export/download?{forbidden_query}")
        self.assertEqual(status, 403)

    def test_self_hosted_billing_state_and_history(self):
        status, billing, body = self.user_a["api"].request("GET", "/api/_billing")
        self.assertEqual(status, 200, body)
        self.assertIn(billing.get("state"), ("OK", "OVERUSE"))
        self.assertIsInstance(billing.get("usage"), (int, float))
        self.assertGreaterEqual(billing["usage"], 0)
        self.assertGreaterEqual(billing.get("month", 0), 1)
        self.assertGreater(billing.get("year", 0), 2020)
        self.assertIsInstance(billing.get("plan"), dict)
        self.assertGreater(billing["plan"].get("monthlyEvents", 0), 0)
        self.assertEqual(billing["plan"].get("monthlyPrice"), 0)
        self.assertIsNone(billing.get("subscription"))

        status, history, body = self.user_a["api"].request("GET", "/api/_billing/historical")
        self.assertEqual(status, 200, body)
        self.assertIsInstance(history, list)
        for row in history:
            self.assertIn("date", row)
            self.assertIn("events", row)
            self.assertGreaterEqual(row["events"], 0)

    def test_shares_and_ownership_request_lifecycle(self):
        observed_mismatches = []
        app_id = self.app_a["id"]
        status, app, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}")
        self.assertEqual(status, 200, body)
        self.assertTrue(app.get("hasOwnership"))

        status, info, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}/shares/info")
        self.assertEqual(status, 200, body)
        self.assertTrue(info.get("hasOwnership"))
        self.assertFalse(info.get("sharedWithCurrentUser"))
        if info.get("numberOfOtherShares") != 0:
            observed_mismatches.append(
                f"empty shares/info count should be 0, got {info.get('numberOfOtherShares')}"
            )

        status, shares, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}/shares")
        self.assertEqual(status, 200, body)
        self.assertEqual(shares, [])
        status, _, _ = self.user_b["api"].request("GET", f"/api/_apps/{app_id}/shares")
        self.assertEqual(status, 404)

        email = f"compat-{uuid.uuid4().hex[:12]}@example.invalid"
        share_path = f"/api/_apps/{app_id}/shares/{quote(email, safe='')}"
        status, _, body = self.user_a["api"].request("PUT", share_path)
        self.assertEqual(status, 200, body)
        status, _, body = self.user_a["api"].request("PUT", share_path)
        self.assertEqual(status, 200, body)
        status, shares, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}/shares")
        self.assertEqual(status, 200, body)
        self.assertEqual(len(shares), 1)
        self.assertEqual(shares[0].get("email"), email)
        status, other_apps, body = self.user_b["api"].request("GET", "/api/_apps")
        self.assertEqual(status, 200, body)
        self.assertNotIn(app_id, {item.get("id") for item in other_apps})

        status, info, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}/shares/info")
        self.assertEqual(status, 200, body)
        self.assertEqual(info.get("numberOfOtherShares"), 1)
        status, _, body = self.user_a["api"].request("DELETE", share_path)
        self.assertEqual(status, 200, body)
        status, shares, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}/shares")
        self.assertEqual(status, 200, body)
        self.assertEqual(shares, [])

        target_email = f"compat-owner-{uuid.uuid4().hex[:12]}@example.invalid"
        request_path = f"/api/_apps/{app_id}/requests"
        request_body = {"targetUserEmail": target_email, "purpose": "AppOwnership"}
        status, _, body = self.user_a["api"].request("POST", request_path, request_body)
        self.assertEqual(status, 200, body)
        status, pending, body = self.user_a["api"].request("GET", f"{request_path}?purpose=AppOwnership")
        self.assertEqual(status, 200, body)
        self.assertEqual(pending.get("targetUserEmail"), target_email)
        self.assertEqual(pending.get("status"), "pending")

        status, _, _ = self.user_a["api"].request("POST", request_path, request_body)
        self.assertEqual(status, 400)
        status, _, _ = self.user_b["api"].request(
            "POST", f"{request_path}/accept?purpose=AppOwnership"
        )
        self.assertEqual(status, 404)
        status, _, body = self.user_a["api"].request(
            "DELETE", f"{request_path}?purpose=AppOwnership"
        )
        if status != 200:
            observed_mismatches.append(f"cancel ownership request should return 200, got {status}: {body}")
        status, pending, body = self.user_a["api"].request("GET", f"{request_path}?purpose=AppOwnership")
        self.assertIn(status, (200, 204), body)
        self.assertIsNone(pending)
        status, current_app, body = self.user_a["api"].request("GET", f"/api/_apps/{app_id}")
        self.assertEqual(status, 200, body)
        self.assertTrue(current_app.get("hasOwnership"))

        # Username/password registration has no email, so a positive recipient-side
        # accept/share test needs an OAuth/email fixture outside this HTTP-only suite.
        self.assertFalse(self.user_b["user"].get("email"))
        self.assertFalse(observed_mismatches, "; ".join(observed_mismatches))

    def test_feature_flag_management_and_sdk_reads(self):
        status, flags, body = self.user_a["api"].request("GET", f"/api/_flags/{self.app_a['id']}")
        self.assertEqual(status, 200, body)
        self.assertEqual(flags, [])

        status, _, _ = self.user_b["api"].request("GET", f"/api/_flags/{self.app_a['id']}")
        self.assertEqual(status, 404)

        key = f"compat-{uuid.uuid4().hex[:12]}"
        value = "enabled-from-http-contract"
        status, _, body = self.user_a["api"].request(
            "POST",
            "/api/_flags",
            {"appId": self.app_a["id"], "key": key, "value": value, "environment": "release"},
        )
        self.assertEqual(status, 200, body)

        status, flags, body = self.user_a["api"].request("GET", f"/api/_flags/{self.app_a['id']}")
        self.assertEqual(status, 200, body)
        self.assertTrue(any(flag.get("key") == key and flag.get("value") == value for flag in flags))

        sdk_headers = {"App-Key": self.app_a["appKey"].lower()}
        sdk_body = {"systemProps": {}, "props": {}}
        status, flag, body = self.user_a["api"].request(
            "GET", f"/api/v0/feature-flags/{key}", sdk_body, sdk_headers
        )
        self.assertEqual(status, 200, body)
        self.assertEqual(flag.get("key"), key)
        self.assertEqual(flag.get("value"), value)

    def test_event_ingestion_status_and_app_key_precedes_json_parsing(self):
        headers = {"App-Key": self.app_a["appKey"], "CloudFront-Viewer-Address": "198.51.100.10"}
        event = {
            "eventName": "Compat Event",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "sessionId": "1234-5678-9012-3456",
            "systemProps": {
                "isDebug": False,
                "osName": "macOS",
                "osVersion": "14.0",
                "appVersion": "1.0.0",
                "sdkVersion": "compat-http-test",
            },
            "props": {"source": "black-box", "count": 1},
        }
        status, _, body = self.user_a["api"].request("POST", "/api/v0/event", event, headers)
        self.assertEqual(status, 200, body)

        invalid_event = dict(event, props=["not", "an", "object"])
        status, _, _ = self.user_a["api"].request("POST", "/api/v0/event", invalid_event, headers)
        self.assertEqual(status, 400)

        unknown_headers = dict(headers, **{"App-Key": "THIS-DOES-NOT-EXIST"})
        status, _, _ = self.user_a["api"].request(
            "POST", "/api/v0/event", headers=unknown_headers, raw_body="{ this is not json"
        )
        self.assertEqual(status, 200)

    def test_error_ingestion_validation_and_readback(self):
        headers = {"App-Key": self.app_a["appKey"], "CloudFront-Viewer-Address": "198.51.100.11"}
        error_type = f"CompatError{uuid.uuid4().hex[:10]}"
        error = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "errorType": error_type,
            "errorMessage": "Compat failure reported by test@example.com",
            "stackTrace": "at Compat.Test() for test@example.com",
            "platform": "compat-test",
            "osName": "Linux",
            "osVersion": "1",
            "appVersion": "1.0.0",
            "sdkVersion": "compat-http-test",
            "sessionId": "1234-5678-9012-3456",
            "severity": "error",
            "kind": "handled",
            "isDebug": False,
        }
        status, _, body = self.user_a["api"].request("POST", "/api/v0/error", error, headers)
        self.assertEqual(status, 202, body)

        invalid = dict(error)
        invalid.pop("errorMessage")
        status, _, _ = self.user_a["api"].request("POST", "/api/v0/error", invalid, headers)
        self.assertEqual(status, 400)

        unknown_headers = dict(headers, **{"App-Key": "THIS-DOES-NOT-EXIST"})
        status, _, _ = self.user_a["api"].request(
            "POST", "/api/v0/error", headers=unknown_headers, raw_body="{ this is not json"
        )
        self.assertEqual(status, 401)

        deadline = time.monotonic() + 20
        result = None
        while time.monotonic() < deadline:
            status, result, body = self.user_a["api"].request(
                "GET", f"/api/v0/apps/{self.app_a['id']}/errors?buildMode=release"
            )
            self.assertEqual(status, 200, body)
            if isinstance(result, dict) and any(
                item.get("errorType") == error_type for item in result.get("errors", [])
            ):
                break
            time.sleep(1)
        else:
            self.fail(f"accepted error was not readable after async flush: {result}")

        stored = next(item for item in result["errors"] if item.get("errorType") == error_type)
        self.assertEqual(stored.get("errorMessage"), "Compat failure reported by [EMAIL_REDACTED]")
        self.assertEqual(stored.get("stackTrace"), "at Compat.Test() for [EMAIL_REDACTED]")

        filtered_query = urlencode(
            {"buildMode": "release", "errorType": error_type, "severity": "error", "kind": "handled", "osName": "Linux"}
        )
        status, filtered, body = self.user_a["api"].request(
            "GET", f"/api/v0/apps/{self.app_a['id']}/errors?{filtered_query}"
        )
        self.assertEqual(status, 200, body)
        self.assertEqual(filtered.get("pagination", {}).get("total"), 1)
        self.assertEqual(len(filtered.get("errors", [])), 1)

        error_id = stored.get("errorId")
        status, detail, body = self.user_a["api"].request(
            "GET", f"/api/v0/apps/{self.app_a['id']}/errors/{error_id}?buildMode=release"
        )
        self.assertEqual(status, 200, body)
        self.assertEqual(detail.get("errorId"), error_id)
        self.assertEqual(detail.get("errorType"), error_type)
        self.assertEqual(detail.get("errorMessage"), "Compat failure reported by [EMAIL_REDACTED]")

        status, _, _ = self.user_a["api"].request(
            "GET", f"/api/v0/apps/{self.app_a['id']}/errors/{uuid.uuid4()}?buildMode=release"
        )
        self.assertEqual(status, 404)
        status, _, _ = self.user_b["api"].request(
            "GET", f"/api/v0/apps/{self.app_a['id']}/errors/{error_id}?buildMode=release"
        )
        self.assertEqual(status, 403)


if __name__ == "__main__":
    unittest.main(verbosity=2)
