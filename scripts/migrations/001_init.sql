CREATE TABLE blueprints (
    id         UUID        PRIMARY KEY,
    name       TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ
);

CREATE TABLE blueprint_tasks (
    id             UUID        PRIMARY KEY,
    blueprint_id   UUID        NOT NULL REFERENCES blueprints (id),
    blueprint_name TEXT        NOT NULL,
    key            TEXT        NOT NULL,
    status       TEXT        NOT NULL CHECK (status IN ('pending', 'dispatched', 'running', 'success', 'failed')),
    payload      JSONB,
    result       JSONB,
    error        TEXT,
    locked_until TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_task_dependencies (
    task_id       UUID NOT NULL REFERENCES blueprint_tasks (id),
    depends_on_id UUID NOT NULL REFERENCES blueprint_tasks (id),
    PRIMARY KEY (task_id, depends_on_id)
);

CREATE INDEX idx_task_deps_depends_on_id ON blueprint_task_dependencies (depends_on_id);
CREATE INDEX idx_blueprint_tasks_status ON blueprint_tasks (status);
CREATE INDEX idx_blueprint_tasks_blueprint_status ON blueprint_tasks (blueprint_id, status);
CREATE INDEX idx_blueprint_tasks_locked_until ON blueprint_tasks (locked_until) WHERE status = 'running';
