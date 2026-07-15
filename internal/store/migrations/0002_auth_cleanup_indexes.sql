-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.1.0
CREATE INDEX sessions_idle_expiration_idx ON sessions(julianday(idle_expires_at));
CREATE INDEX sessions_absolute_expiration_idx ON sessions(julianday(absolute_expires_at));
CREATE INDEX login_throttles_window_expiration_idx ON login_throttles(julianday(window_started_at));
