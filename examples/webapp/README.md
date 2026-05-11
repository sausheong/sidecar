# Sidecar Demo Webapp

A minimal Go task-tracker API that exercises every Sidecar signal adapter.
Use this to try out Sidecar before attaching it to a real project.

## What's seeded

| Issue | Where | Sidecar adapter that catches it |
|-------|-------|--------------------------------|
| `DELETE /tasks/{id}` returns 200 instead of 204 | `handler.go` | CI adapter (failing test) |
| `POST /tasks` accepts empty title | `handler.go` | CI adapter (failing test) |
| Missing test for `PUT /tasks/{id}` | `handler_test.go` | Schedule adapter (sweep) |
| ERROR log burst via `/demo/stress` | runtime | Logs adapter |
| HighErrorRate Prometheus alert | runtime | Metrics adapter |

## Run the webapp

```bash
go run ./cmd/webapp
# Server on http://localhost:8080
```

## Run Prometheus (optional — for metrics adapter)

```bash
docker run --rm -p 9090:9090 \
  -v "$PWD/prometheus.yml:/etc/prometheus/prometheus.yml" \
  -v "$PWD/prometheus-rules.yml:/etc/prometheus/prometheus-rules.yml" \
  prom/prometheus
```

Then open http://localhost:9090/alerts.

## Attach Sidecar

```bash
export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5433/sidecar"
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."   # for memory

cd examples/webapp
sidecar attach .
```

## Trigger each adapter manually

```bash
# Git adapter — make any commit
echo "# test" >> README.md && git add README.md && git commit -m "test: trigger git adapter"

# Logs adapter — generate ERROR burst (5 hits within 30s)
for i in $(seq 1 6); do curl -s -X POST http://localhost:8080/demo/stress; done

# Metrics adapter — same stress endpoint drives HighErrorRate alert
# (requires Prometheus running; alert fires after ~30s of >5% error rate)

# On-demand task
sidecar task "review test coverage and add missing tests" --repo .
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
