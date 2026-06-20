CREATE TABLE blueprints (
    id            UUID        PRIMARY KEY,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
);

CREATE TABLE blueprint_nodes (
    id           UUID        PRIMARY KEY,
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    node_type    TEXT        NOT NULL DEFAULT 'task'
                             CHECK (node_type IN ('task', 'signal')),
    pending_deps INT         NOT NULL DEFAULT 0,
    payload      JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_node_dependencies (
    node_id       UUID NOT NULL REFERENCES blueprint_nodes (id),
    depends_on_id UUID NOT NULL REFERENCES blueprint_nodes (id),
    PRIMARY KEY (node_id, depends_on_id)
);

CREATE TABLE node_events (
    id           BIGSERIAL   PRIMARY KEY,
    node_id      UUID        NOT NULL REFERENCES blueprint_nodes (id),
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    type         TEXT        NOT NULL CHECK (type IN (
                     'node_dispatched','node_started','node_completed','node_failed','node_retried'
                 )),
    output       JSONB,
    error        TEXT,
    emitted_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE node_heartbeats (
    node_id    UUID        NOT NULL REFERENCES blueprint_nodes (id),
    emitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_node_deps_depends_on_id          ON blueprint_node_dependencies (depends_on_id);
CREATE INDEX idx_blueprint_nodes_pending_dispatch  ON blueprint_nodes (blueprint_id, pending_deps);
CREATE INDEX idx_blueprint_nodes_signal            ON blueprint_nodes (blueprint_id, key)
    WHERE node_type = 'signal';

-- Idempotency: a node completes exactly once; dispatched/started/failed can repeat (retries)
CREATE UNIQUE INDEX idx_node_events_idempotent ON node_events (node_id, type)
    WHERE type = 'node_completed';

CREATE INDEX idx_node_events_node_id      ON node_events (node_id, type);
CREATE INDEX idx_node_events_blueprint_id ON node_events (blueprint_id);
CREATE INDEX idx_node_heartbeats_node_id  ON node_heartbeats (node_id);
CREATE INDEX idx_node_heartbeats_emitted  ON node_heartbeats (emitted_at);
