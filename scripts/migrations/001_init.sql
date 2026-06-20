CREATE TABLE blueprints (
    id            UUID        PRIMARY KEY,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ
);

CREATE TABLE blueprint_tasks (
    id           UUID        PRIMARY KEY,
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    pending_deps INT         NOT NULL DEFAULT 0,
    payload      JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_task_dependencies (
    task_id       UUID NOT NULL REFERENCES blueprint_tasks (id),
    depends_on_id UUID NOT NULL REFERENCES blueprint_tasks (id),
    PRIMARY KEY (task_id, depends_on_id)
);

CREATE TABLE task_events (
    id           BIGSERIAL   PRIMARY KEY,
    task_id      UUID        NOT NULL REFERENCES blueprint_tasks (id),
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    type         TEXT        NOT NULL CHECK (type IN (
                     'task_dispatched','task_started','task_completed','task_failed','task_retried'
                 )),
    output       JSONB,
    error        TEXT,
    emitted_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE task_heartbeats (
    task_id    UUID        NOT NULL REFERENCES blueprint_tasks (id),
    emitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_task_deps_depends_on_id          ON blueprint_task_dependencies (depends_on_id);
CREATE INDEX idx_blueprint_tasks_pending_dispatch  ON blueprint_tasks (blueprint_id, pending_deps);

-- Idempotency: a task completes exactly once; dispatched/started/failed can repeat (retries)
CREATE UNIQUE INDEX idx_task_events_idempotent ON task_events (task_id, type)
    WHERE type = 'task_completed';

CREATE INDEX idx_task_events_task_id      ON task_events (task_id, type);
CREATE INDEX idx_task_events_blueprint_id ON task_events (blueprint_id);
CREATE INDEX idx_task_heartbeats_task_id  ON task_heartbeats (task_id);
CREATE INDEX idx_task_heartbeats_emitted  ON task_heartbeats (emitted_at);
