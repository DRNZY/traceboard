CREATE INDEX ix_runs_sort_at ON runs(COALESCE(started_at, last_event_at, created_at) DESC, id DESC);

CREATE INDEX ix_steps_run_parent ON steps(run_id, parent_step_id);

CREATE INDEX ix_alerts_unresolved ON alerts(resolved_at, type);

CREATE INDEX ix_quarantine_source ON quarantine(source, created_at);
