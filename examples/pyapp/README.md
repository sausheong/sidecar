# Sidecar Demo — Python Webapp

A minimal Flask task-tracker API that exercises every Sidecar signal adapter.
Mirrors the Go webapp at `../webapp`, but uses Python/Flask and pytest.

## What's seeded

| Issue | Where | Sidecar adapter that catches it |
|-------|-------|--------------------------------|
| `DELETE /tasks/{id}` returns 200 instead of 204 | `app.py` | CI adapter / on-demand task |
| `POST /tasks` accepts empty title | `app.py` | CI adapter / on-demand task |
| `/demo/stress` logs ERROR on every call | `app.py` | Logs adapter |

## Run the webapp

```bash
pip install -r requirements.txt
python app.py
# Server on http://localhost:8081
```

## Run the tests

```bash
pytest test_app.py -v
# Expect 2 failures (seeded bugs):
#   test_create_task_empty_title_rejected
#   test_delete_task_correct_status
```

## Attach Sidecar

```bash
export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5432/sidecar?sslmode=disable"
export ANTHROPIC_API_KEY="sk-ant-..."

# from examples/pyapp/
sidecar attach .
```

## Trigger each adapter

```bash
# Git adapter — make any commit
echo "# test" >> README.md && git add README.md && git commit -m "test: trigger git adapter"

# Logs adapter — generate ERROR burst (5 hits within 30s)
for i in $(seq 1 6); do curl -s -X POST http://localhost:8081/demo/stress; done

# On-demand task
sidecar task "fix the failing tests in test_app.py" --repo .
```

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/tasks` | List all tasks |
| POST | `/tasks` | Create task `{"title":"...","description":"..."}` |
| GET | `/tasks/{id}` | Get task |
| PUT | `/tasks/{id}` | Update task `{"title":"...","description":"..."}` |
| POST | `/tasks/{id}/complete` | Mark task done |
| DELETE | `/tasks/{id}` | Delete task |
| POST | `/demo/stress` | Generate a 500 error (triggers logs adapter) |
| GET | `/metrics` | Prometheus metrics |
