import threading
import uuid
from datetime import datetime, timezone


class Task:
    def __init__(self, title: str, description: str = ""):
        self.id = str(uuid.uuid4())[:8]
        self.title = title
        self.description = description
        self.done = False
        self.created_at = datetime.now(timezone.utc).isoformat()
        self.updated_at = self.created_at

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "title": self.title,
            "description": self.description,
            "done": self.done,
            "created_at": self.created_at,
            "updated_at": self.updated_at,
        }


class Store:
    def __init__(self):
        self._tasks: dict[str, Task] = {}
        self._lock = threading.Lock()

    def list(self) -> list[dict]:
        with self._lock:
            return [t.to_dict() for t in self._tasks.values()]

    def create(self, title: str, description: str = "") -> dict:
        task = Task(title, description)
        with self._lock:
            self._tasks[task.id] = task
        return task.to_dict()

    def get(self, task_id: str) -> dict | None:
        with self._lock:
            t = self._tasks.get(task_id)
            return t.to_dict() if t else None

    def update(self, task_id: str, title: str, description: str) -> dict | None:
        with self._lock:
            t = self._tasks.get(task_id)
            if not t:
                return None
            t.title = title
            t.description = description
            t.updated_at = datetime.now(timezone.utc).isoformat()
            return t.to_dict()

    def complete(self, task_id: str) -> dict | None:
        with self._lock:
            t = self._tasks.get(task_id)
            if not t:
                return None
            t.done = True
            t.updated_at = datetime.now(timezone.utc).isoformat()
            return t.to_dict()

    def delete(self, task_id: str) -> bool:
        with self._lock:
            if task_id not in self._tasks:
                return False
            del self._tasks[task_id]
            return True

    def count(self) -> int:
        with self._lock:
            return len(self._tasks)
