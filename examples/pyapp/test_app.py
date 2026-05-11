import pytest
from app import app, store


@pytest.fixture(autouse=True)
def fresh_store():
    store._tasks.clear()
    yield


@pytest.fixture
def client():
    app.config["TESTING"] = True
    with app.test_client() as c:
        yield c


# ── Health ────────────────────────────────────────────────────────────────────

def test_health(client):
    r = client.get("/health")
    assert r.status_code == 200
    assert r.get_json()["status"] == "ok"


# ── List ──────────────────────────────────────────────────────────────────────

def test_list_empty(client):
    r = client.get("/tasks")
    assert r.status_code == 200
    assert r.get_json() == []


def test_list_with_tasks(client):
    store.create("a", "")
    store.create("b", "")
    r = client.get("/tasks")
    assert r.status_code == 200
    assert len(r.get_json()) == 2


# ── Create ────────────────────────────────────────────────────────────────────

def test_create_task(client):
    r = client.post("/tasks", json={"title": "buy milk", "description": "whole"})
    assert r.status_code == 201
    data = r.get_json()
    assert data["title"] == "buy milk"
    assert data["done"] is False
    assert "id" in data


def test_create_task_invalid_json(client):
    r = client.post("/tasks", data="not json", content_type="text/plain")
    assert r.status_code == 400


def test_create_task_empty_title_rejected(client):
    # FAILS until the empty-title bug is fixed
    r = client.post("/tasks", json={"title": "", "description": "no title"})
    assert r.status_code == 400, "empty title should be rejected with 400"


def test_create_task_whitespace_title_rejected(client):
    # FAILS until the empty-title bug is fixed
    r = client.post("/tasks", json={"title": "   ", "description": "spaces"})
    assert r.status_code == 400, "whitespace-only title should be rejected with 400"


# ── Get ───────────────────────────────────────────────────────────────────────

def test_get_task_found(client):
    task = store.create("test", "")
    r = client.get(f"/tasks/{task['id']}")
    assert r.status_code == 200
    assert r.get_json()["id"] == task["id"]


def test_get_task_not_found(client):
    r = client.get("/tasks/doesnotexist")
    assert r.status_code == 404


# ── Update ────────────────────────────────────────────────────────────────────

def test_update_task(client):
    task = store.create("original", "old")
    r = client.put(f"/tasks/{task['id']}", json={"title": "updated", "description": "new"})
    assert r.status_code == 200
    data = r.get_json()
    assert data["title"] == "updated"
    assert data["description"] == "new"


def test_update_task_not_found(client):
    r = client.put("/tasks/ghost", json={"title": "x", "description": ""})
    assert r.status_code == 404


def test_update_task_invalid_json(client):
    task = store.create("x", "")
    r = client.put(f"/tasks/{task['id']}", data="bad", content_type="text/plain")
    assert r.status_code == 400


# ── Complete ──────────────────────────────────────────────────────────────────

def test_complete_task(client):
    task = store.create("finish me", "")
    r = client.post(f"/tasks/{task['id']}/complete")
    assert r.status_code == 200
    assert r.get_json()["done"] is True


def test_complete_task_not_found(client):
    r = client.post("/tasks/ghost/complete")
    assert r.status_code == 404


# ── Delete ────────────────────────────────────────────────────────────────────

def test_delete_task_correct_status(client):
    task = store.create("delete me", "")
    r = client.delete(f"/tasks/{task['id']}")
    # FAILS until the 200→204 bug is fixed
    assert r.status_code == 204, f"DELETE should return 204, got {r.status_code}"


def test_delete_task_not_found(client):
    r = client.delete("/tasks/ghost")
    assert r.status_code == 404


def test_delete_task_removes_from_list(client):
    t1 = store.create("keep", "")
    t2 = store.create("gone", "")
    client.delete(f"/tasks/{t2['id']}")
    r = client.get("/tasks")
    ids = [t["id"] for t in r.get_json()]
    assert t1["id"] in ids
    assert t2["id"] not in ids
