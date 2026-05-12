# Sidecar Demo — Python Webapp

A minimal Flask task-tracker API that exercises every Sidecar signal adapter.
Mirrors the Go webapp at `../goapp`, but uses Python/Flask and pytest.

## Before you start — copy this out

**Do not run Sidecar against this directory inside the sidecar repo.** The git
adapter records the repo root's HEAD at startup and watches for new commits.
If you run it here, every commit you make to the sidecar repo itself will
trigger the agent.

Copy the example to its own directory and initialise a fresh git repo:

```bash
cp -r examples/pyapp ~/sidecar-pyapp
cd ~/sidecar-pyapp
git init
git add .
git commit -m "initial: seeded demo app"
```

Then follow the steps below from inside `~/sidecar-pyapp`.

## What's seeded

| Issue | Where | Sidecar adapter that catches it |
|-------|-------|--------------------------------|
| `DELETE /tasks/{id}` returns 200 instead of 204 | `app.py` | On-demand task / git adapter |
| `POST /tasks` accepts empty title | `app.py` | On-demand task / git adapter |
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
# Expect 3 failures (seeded bugs):
#   test_create_task_empty_title_rejected
#   test_create_task_whitespace_title_rejected
#   test_delete_task_correct_status
```

## Start the Sidecar database

Sidecar needs PostgreSQL with the pgvector extension. The quickest way:

```bash
docker run -d --name sidecar-db \
  -e POSTGRES_USER=sidecar \
  -e POSTGRES_PASSWORD=sidecar \
  -e POSTGRES_DB=sidecar \
  -p 5432:5432 \
  pgvector/pgvector:pg17
```

Or point `SIDECAR_DB_URL` at any existing PostgreSQL 15+ instance that has
pgvector — including Neon (free tier has pgvector built in).

## Attach Sidecar

```bash
export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5432/sidecar?sslmode=disable"
export ANTHROPIC_API_KEY="sk-ant-..."

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
