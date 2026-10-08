package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Run struct {
	ID       int64     `json:"id"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Trigger  string    `json:"trigger"`
	Status   string    `json:"status"`
	Summary  string    `json:"summary"`
	// Size and Files total the run's snapshots.
	Size    int64       `json:"size"`
	Files   int64       `json:"files"`
	Sources []RunSource `json:"sources,omitempty"`
}

type RunSource struct {
	RunID     int64     `json:"run_id"`
	Name      string    `json:"name"`
	Strategy  string    `json:"strategy"`
	Status    string    `json:"status"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished"`
	Size      int64     `json:"size"`
	Files     int64     `json:"files"`
	Databases int       `json:"databases"`
	Message   string    `json:"message"`
	Snapshots []string  `json:"snapshots"`
}

type LogLine struct {
	ID    int64     `json:"id"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

func (s *Store) StartRun(ctx context.Context, trigger string, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (started, trigger, status) VALUES (?, ?, 'running')`, ms(at), trigger)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(ctx context.Context, id int64, status, summary string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, summary = ?, finished = ? WHERE id = ?`, status, summary, ms(at), id)
	return err
}

// AbandonRuns marks runs left running by a restart as failed.
func (s *Store) AbandonRuns(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE run_sources SET status = 'failed', message = 'Keep stopped during the run', finished = ?
		 WHERE status = 'running'`, ms(at))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE runs SET status = 'failed', summary = 'Keep stopped during the run', finished = ?
		 WHERE status = 'running'`, ms(at))
	return err
}

// SaveRunSource inserts or replaces a source's result in a run.
func (s *Store) SaveRunSource(ctx context.Context, rs RunSource) error {
	snaps, _ := json.Marshal(nonNil(rs.Snapshots))
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO run_sources (run_id, name, strategy, status, started, finished, size, files, databases, message, snapshots)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, name) DO UPDATE SET strategy = excluded.strategy, status = excluded.status,
		   started = excluded.started, finished = excluded.finished, size = excluded.size, files = excluded.files,
		   databases = excluded.databases, message = excluded.message, snapshots = excluded.snapshots`,
		rs.RunID, rs.Name, rs.Strategy, rs.Status, ms(rs.Started), ms(rs.Finished), rs.Size, rs.Files,
		rs.Databases, rs.Message, string(snaps))
	return err
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *Store) Log(ctx context.Context, runID int64, level, text string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO run_log (run_id, at, level, text) VALUES (?, ?, ?, ?)`, runID, ms(time.Now()), level, text)
	return err
}

const runCols = `r.id, r.started, r.finished, r.trigger, r.status, r.summary,
	COALESCE((SELECT SUM(size) FROM run_sources WHERE run_id = r.id), 0),
	COALESCE((SELECT SUM(files) FROM run_sources WHERE run_id = r.id), 0)`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var started, finished int64
	err := sc.Scan(&r.ID, &started, &finished, &r.Trigger, &r.Status, &r.Summary, &r.Size, &r.Files)
	r.Started, r.Finished = fromMS(started), fromMS(finished)
	return r, err
}

// Runs lists runs, newest first, before the run id before (0: from the
// newest).
func (s *Store) Runs(ctx context.Context, before int64, limit int) ([]Run, error) {
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runCols+` FROM runs r WHERE r.id < ? ORDER BY r.id DESC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun reads a run with its sources.
func (s *Store) GetRun(ctx context.Context, id int64) (Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs r WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	r.Sources, err = s.querySources(ctx, `WHERE run_id = ? ORDER BY name`, id)
	return r, err
}

// LastRun is the newest run; ok is false when there's none.
func (s *Store) LastRun(ctx context.Context) (Run, bool, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs r ORDER BY r.id DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	return r, err == nil, err
}

func (s *Store) querySources(ctx context.Context, where string, args ...any) ([]RunSource, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, name, strategy, status, started, finished, size, files, databases, message, snapshots
		 FROM run_sources `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunSource{}
	for rows.Next() {
		var rs RunSource
		var started, finished int64
		var snaps string
		if err := rows.Scan(&rs.RunID, &rs.Name, &rs.Strategy, &rs.Status, &started, &finished,
			&rs.Size, &rs.Files, &rs.Databases, &rs.Message, &snaps); err != nil {
			return nil, err
		}
		rs.Started, rs.Finished = fromMS(started), fromMS(finished)
		_ = json.Unmarshal([]byte(snaps), &rs.Snapshots)
		out = append(out, rs)
	}
	return out, rows.Err()
}

// SourceHistory is each source's latest attempt and latest success (ok
// or warn: the snapshot was taken).
type SourceHistory struct {
	Latest  *RunSource `json:"latest,omitempty"`
	Success *RunSource `json:"success,omitempty"`
}

func (s *Store) SourceHistories(ctx context.Context) (map[string]SourceHistory, error) {
	latest, err := s.querySources(ctx,
		`WHERE (run_id, name) IN (SELECT MAX(run_id), name FROM run_sources GROUP BY name)`)
	if err != nil {
		return nil, err
	}
	success, err := s.querySources(ctx,
		`WHERE (run_id, name) IN (SELECT MAX(run_id), name FROM run_sources WHERE status IN ('ok', 'warn') GROUP BY name)`)
	if err != nil {
		return nil, err
	}
	out := map[string]SourceHistory{}
	for i := range latest {
		h := out[latest[i].Name]
		h.Latest = &latest[i]
		out[latest[i].Name] = h
	}
	for i := range success {
		h := out[success[i].Name]
		h.Success = &success[i]
		out[success[i].Name] = h
	}
	return out, nil
}

// SourceSizes is a source's snapshot size per successful run, oldest
// first, for its size over time.
func (s *Store) SourceSizes(ctx context.Context, name string, limit int) ([]RunSource, error) {
	list, err := s.querySources(ctx,
		`WHERE name = ? AND status IN ('ok', 'warn') ORDER BY run_id DESC LIMIT ?`, name, limit)
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, err
}

// RunLog lists a run's log lines after the line id after.
func (s *Store) RunLog(ctx context.Context, runID, after int64) ([]LogLine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, level, text FROM run_log WHERE run_id = ? AND id > ? ORDER BY id LIMIT 2000`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		var at int64
		if err := rows.Scan(&l.ID, &at, &l.Level, &l.Text); err != nil {
			return nil, err
		}
		l.At = fromMS(at)
		out = append(out, l)
	}
	return out, rows.Err()
}

// Prune drops logs older than logsFor and runs older than runsFor.
func (s *Store) Prune(ctx context.Context, now time.Time, logsFor, runsFor time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM run_log WHERE run_id IN (SELECT id FROM runs WHERE started < ?)`, ms(now.Add(-logsFor)))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM runs WHERE started < ? AND status != 'running'`, ms(now.Add(-runsFor)))
	return err
}
