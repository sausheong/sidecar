import logging
import os
import time

from flask import Flask, jsonify, request
from prometheus_client import Counter, Gauge, generate_latest, CONTENT_TYPE_LATEST

from store import Store

# ── Logging ──────────────────────────────────────────────────────────────────
os.makedirs("logs", exist_ok=True)
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
    handlers=[
        logging.StreamHandler(),
        logging.FileHandler("logs/app.log"),
    ],
)
log = logging.getLogger(__name__)

# ── App + store ───────────────────────────────────────────────────────────────
app = Flask(__name__)
store = Store()

# ── Metrics ───────────────────────────────────────────────────────────────────
tasks_total = Gauge("tasks_total", "Number of tasks in the store")
requests_total = Counter("http_requests_total", "Total HTTP requests", ["method", "path", "status"])


def track(status: int):
    requests_total.labels(
        method=request.method,
        path=request.path,
        status=str(status),
    ).inc()


# ── Routes ────────────────────────────────────────────────────────────────────

@app.get("/health")
def health():
    return jsonify({"status": "ok"})


@app.get("/metrics")
def metrics():
    return generate_latest(), 200, {"Content-Type": CONTENT_TYPE_LATEST}


@app.get("/tasks")
def list_tasks():
    tasks = store.list()
    tasks_total.set(store.count())
    track(200)
    return jsonify(tasks), 200


@app.post("/tasks")
def create_task():
    data = request.get_json(silent=True)
    if not data:
        track(400)
        return jsonify({"error": "invalid JSON"}), 400

    title = data.get("title", "")
    # FIX (1): reject empty or whitespace-only titles
    if not title.strip():
        track(400)
        return jsonify({"error": "title is required"}), 400

    description = data.get("description", "")
    task = store.create(title, description)
    tasks_total.set(store.count())
    track(201)
    return jsonify(task), 201


@app.get("/tasks/<task_id>")
def get_task(task_id):
    task = store.get(task_id)
    if task is None:
        track(404)
        return jsonify({"error": "not found"}), 404
    track(200)
    return jsonify(task), 200


@app.put("/tasks/<task_id>")
def update_task(task_id):
    data = request.get_json(silent=True)
    if not data:
        track(400)
        return jsonify({"error": "invalid JSON"}), 400
    title = data.get("title", "")
    description = data.get("description", "")
    task = store.update(task_id, title, description)
    if task is None:
        track(404)
        return jsonify({"error": "not found"}), 404
    track(200)
    return jsonify(task), 200


@app.post("/tasks/<task_id>/complete")
def complete_task(task_id):
    task = store.complete(task_id)
    if task is None:
        track(404)
        return jsonify({"error": "not found"}), 404
    track(200)
    return jsonify(task), 200


@app.delete("/tasks/<task_id>")
def delete_task(task_id):
    if not store.delete(task_id):
        track(404)
        return jsonify({"error": "not found"}), 404
    tasks_total.set(store.count())
    # FIX (2): return 204 No Content with an empty body
    track(204)
    return "", 204


@app.post("/demo/stress")
def stress():
    log.error("simulated server error for Sidecar demo")
    track(500)
    return jsonify({"error": "simulated server error for Sidecar demo"}), 500


if __name__ == "__main__":
    log.info("starting pyapp on :8081")
    app.run(host="0.0.0.0", port=8081, debug=False)
