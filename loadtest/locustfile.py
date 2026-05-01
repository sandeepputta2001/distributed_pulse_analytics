"""
PulseAnalytics Load Test — Locust
Simulates realistic event ingestion and query traffic against Minikube.

Usage (headless, against gateway):
  locust -f locustfile.py --headless -u 50 -r 5 --run-time 5m \
         --host http://$(minikube ip):$(kubectl get svc gateway -n pulse -o jsonpath='{.spec.ports[0].nodePort}')

Usage (web UI):
  locust -f locustfile.py --host http://<gateway-nodeport-url>
  # open http://localhost:8089

Install:
  pip install locust
"""

import random
import time
import uuid

from locust import HttpUser, TaskSet, between, events, task


# ─── Shared test data ─────────────────────────────────────────────────────────

EVENT_TYPES = [
    "page_view", "button_click", "form_submit", "purchase",
    "add_to_cart", "search", "video_play", "video_pause",
    "signup", "login", "logout", "app_open", "app_close",
]

PLATFORMS = ["web", "ios", "android"]
BROWSERS  = ["chrome", "firefox", "safari", "edge"]
COUNTRIES = ["US", "IN", "GB", "DE", "FR", "JP", "BR", "CA"]

APP_IDS  = [f"app_{i:04d}" for i in range(1, 6)]
USER_IDS = [str(uuid.uuid4()) for _ in range(500)]


def make_event(app_id: str) -> dict:
    return {
        "event_id":   str(uuid.uuid4()),
        "event_type": random.choice(EVENT_TYPES),
        "app_id":     app_id,
        "user_id":    random.choice(USER_IDS),
        "device_id":  str(uuid.uuid4()),
        "timestamp":  int(time.time() * 1000),
        "properties": {
            "page":       f"/page/{random.randint(1, 50)}",
            "referrer":   random.choice(["google", "direct", "twitter", "email"]),
            "value":      round(random.uniform(0, 500), 2),
            "platform":   random.choice(PLATFORMS),
            "browser":    random.choice(BROWSERS),
            "country":    random.choice(COUNTRIES),
            "session_id": str(uuid.uuid4()),
        },
    }


def make_batch(app_id: str, size: int = None) -> dict:
    if size is None:
        size = random.choices(
            [1, 5, 10, 25, 50, 100],
            weights=[30, 25, 20, 15, 7, 3],
        )[0]
    return {
        "app_id": app_id,
        "events": [make_event(app_id) for _ in range(size)],
    }


# ─── Task Sets ────────────────────────────────────────────────────────────────

class IngestTasks(TaskSet):
    """Simulates SDK clients sending event batches to the gateway."""

    def on_start(self):
        self.app_id = random.choice(APP_IDS)
        self.headers = {
            "Content-Type": "application/json",
            "X-API-Key":    f"test-api-key-{self.app_id}",
        }

    @task(70)
    def ingest_small_batch(self):
        """Most common: small batch of 1-10 events."""
        payload = make_batch(self.app_id, size=random.randint(1, 10))
        with self.client.post(
            "/v1/events",
            json=payload,
            headers=self.headers,
            catch_response=True,
            name="/v1/events [small]",
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            elif resp.status_code == 429:
                resp.failure(f"Rate limited: {resp.text[:100]}")
            else:
                resp.failure(f"Unexpected {resp.status_code}: {resp.text[:100]}")

    @task(20)
    def ingest_medium_batch(self):
        """Medium batch: 25-100 events."""
        payload = make_batch(self.app_id, size=random.randint(25, 100))
        with self.client.post(
            "/v1/events",
            json=payload,
            headers=self.headers,
            catch_response=True,
            name="/v1/events [medium]",
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            elif resp.status_code == 429:
                resp.failure(f"Rate limited: {resp.text[:100]}")
            else:
                resp.failure(f"Unexpected {resp.status_code}: {resp.text[:100]}")

    @task(5)
    def ingest_large_batch(self):
        """Occasional large batch: 200-500 events."""
        payload = make_batch(self.app_id, size=random.randint(200, 500))
        with self.client.post(
            "/v1/events",
            json=payload,
            headers=self.headers,
            catch_response=True,
            name="/v1/events [large]",
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            elif resp.status_code == 429:
                resp.failure(f"Rate limited: {resp.text[:100]}")
            else:
                resp.failure(f"Unexpected {resp.status_code}: {resp.text[:100]}")

    @task(5)
    def health_check(self):
        self.client.get("/health", name="/health")


class QueryTasks(TaskSet):
    """Simulates dashboard users querying the Query API."""

    def on_start(self):
        self.app_id = random.choice(APP_IDS)
        self.headers = {
            "Authorization": "Bearer dev-token",
            "Content-Type":  "application/json",
        }

    @task(40)
    def query_event_count(self):
        params = {
            "app_id":     self.app_id,
            "event_type": random.choice(EVENT_TYPES),
            "start_time": int((time.time() - 3600) * 1000),
            "end_time":   int(time.time() * 1000),
        }
        self.client.get(
            "/v1/analytics/events/count",
            params=params,
            headers=self.headers,
            name="/v1/analytics/events/count",
        )

    @task(30)
    def query_active_users(self):
        params = {
            "app_id":      self.app_id,
            "granularity": random.choice(["hourly", "daily"]),
            "start_time":  int((time.time() - 86400) * 1000),
            "end_time":    int(time.time() * 1000),
        }
        self.client.get(
            "/v1/analytics/users/active",
            params=params,
            headers=self.headers,
            name="/v1/analytics/users/active",
        )

    @task(20)
    def query_retention(self):
        params = {
            "app_id":     self.app_id,
            "start_time": int((time.time() - 7 * 86400) * 1000),
            "end_time":   int(time.time() * 1000),
            "window":     "7d",
        }
        self.client.get(
            "/v1/analytics/retention",
            params=params,
            headers=self.headers,
            name="/v1/analytics/retention",
        )

    @task(10)
    def health_check(self):
        self.client.get("/health", name="/health [query-api]")


# ─── User Classes ─────────────────────────────────────────────────────────────

class SDKUser(HttpUser):
    """Simulates mobile/web SDK clients sending events to the Gateway."""
    tasks     = [IngestTasks]
    wait_time = between(0.1, 1.0)
    weight    = 80


class DashboardUser(HttpUser):
    """Simulates dashboard users running analytics queries against Query API."""
    tasks     = [QueryTasks]
    wait_time = between(1.0, 5.0)
    weight    = 20


# ─── Event hooks ──────────────────────────────────────────────────────────────

@events.request.add_listener
def on_request(request_type, name, response_time, response_length, exception, **kwargs):
    if response_time and response_time > 500:
        print(f"[SLOW] {request_type} {name}: {response_time:.0f}ms")


@events.test_start.add_listener
def on_test_start(environment, **kwargs):
    print("=" * 60)
    print("PulseAnalytics Load Test Starting")
    print(f"  Target: {environment.host}")
    print(f"  App IDs: {APP_IDS}")
    print("=" * 60)
