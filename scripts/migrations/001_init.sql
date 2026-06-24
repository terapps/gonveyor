CREATE TABLE blueprints (
    id            UUID        PRIMARY KEY,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_units (
    id           UUID        PRIMARY KEY,
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    unit_type    TEXT        NOT NULL DEFAULT 'task'
                             CHECK (unit_type IN ('task', 'signal')),
    pending_deps INT         NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blueprint_unit_dependencies (
    unit_id       UUID NOT NULL REFERENCES blueprint_units (id),
    depends_on_id UUID NOT NULL REFERENCES blueprint_units (id),
    PRIMARY KEY (unit_id, depends_on_id)
);

CREATE TABLE unit_events (
    id           BIGSERIAL   PRIMARY KEY,
    unit_id      UUID        NOT NULL REFERENCES blueprint_units (id),
    blueprint_id UUID        NOT NULL REFERENCES blueprints (id),
    key          TEXT        NOT NULL,
    type         TEXT        NOT NULL CHECK (type IN (
                     'unit_seeded','unit_dispatched','unit_started','unit_completed','unit_failed','unit_retried'
                 )),
    output       JSONB,
    error        TEXT,
    emitted_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE unit_heartbeats (
    unit_id    UUID        NOT NULL REFERENCES blueprint_units (id),
    emitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_unit_deps_depends_on_id          ON blueprint_unit_dependencies (depends_on_id);
CREATE INDEX idx_blueprint_units_pending_dispatch  ON blueprint_units (blueprint_id, pending_deps);
CREATE INDEX idx_blueprint_units_signal            ON blueprint_units (blueprint_id, key)
    WHERE unit_type = 'signal';

-- Idempotency: a unit completes exactly once; dispatched/started/failed can repeat (retries)
CREATE UNIQUE INDEX idx_unit_events_idempotent ON unit_events (unit_id, type)
    WHERE type = 'unit_completed';

CREATE INDEX idx_unit_events_unit_id      ON unit_events (unit_id, type);
CREATE INDEX idx_unit_events_blueprint_id ON unit_events (blueprint_id);
CREATE INDEX idx_unit_heartbeats_unit_id  ON unit_heartbeats (unit_id);
CREATE INDEX idx_unit_heartbeats_emitted  ON unit_heartbeats (emitted_at);

-- UI indexes
CREATE INDEX idx_blueprints_created_at ON blueprints (created_at DESC, id DESC);
CREATE INDEX idx_unit_events_unit_id_emitted ON unit_events (unit_id, emitted_at DESC) INCLUDE (type);
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_blueprints_name_trgm ON blueprints USING gin (name gin_trgm_ops);
