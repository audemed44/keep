import { api } from "../api";
import { useData } from "../hooks";
import { ago, bytes, plural, RUN_LABEL, runTone, STATE_LABEL, stateTone, took } from "../lib";
import { Dot, Empty, ErrorNote, Figure, SectionHead } from "./ui";

const STRATEGY: Record<string, string> = {
  sqlite: "Files, with every SQLite database copied consistently (VACUUM INTO)",
  files: "Plain files, nothing prepared",
  postgres: "pg_dumpall in the database's container",
  mariadb: "mariadb-dump in the database's container",
  stop: "Containers stopped for the snapshot, then started again",
};

/** One source: how it's backed up, its size over time, its recent runs. */
export function SourcePage({ name }: { name: string }) {
  const { data: o, error } = useData(api.overview, 10_000);
  const { data: history } = useData(() => api.sourceHistory(name), 0, [name]);
  const s = o?.sources.find((x) => x.name === name);

  const max = Math.max(1, ...(history ?? []).map((h) => h.size));
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">
          <a class="link" href="/">
            Sources
          </a>
        </div>
        <h1 class="page-title">{name}</h1>
        {s && (
          <div class="figures stagger">
            <Figure value={STATE_LABEL[s.state]} label="State" tone={stateTone(s.state)} />
            <Figure value={s.success ? ago(s.success.finished) : "—"} label="Last good backup" />
            <Figure
              value={s.success ? bytes(s.success.size) : "—"}
              label={s.success ? `In ${plural(s.success.files, "file")}` : "Size"}
            />
          </div>
        )}
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {o && !s && <ErrorNote>No source called {name} in keep.yml now.</ErrorNote>}
      {s?.state === "errors" && s.latest?.message && <ErrorNote>{s.latest.message}</ErrorNote>}

      {s && (
        <section class="section">
          <SectionHead index={1} title="Setup" />
          <dl class="kv">
            <dt>Strategy</dt>
            <dd>
              <code>{s.strategy}</code> — {STRATEGY[s.strategy]}
            </dd>
            {s.path && (
              <>
                <dt>Path</dt>
                <dd class="mono">
                  {s.path}
                  {s.host_path && s.host_path !== s.path && (
                    <span class="muted"> (host: {s.host_path})</span>
                  )}
                </dd>
              </>
            )}
            {s.container && (
              <>
                <dt>Container</dt>
                <dd class="mono">{s.container}</dd>
              </>
            )}
            {s.volume && (
              <>
                <dt>Covers</dt>
                <dd class="mono">volume {s.volume}</dd>
              </>
            )}
            <dt>Excludes</dt>
            <dd class="mono">
              {s.excludes?.length ? (
                s.excludes.join("  ")
              ) : (
                <span class="muted">none of its own</span>
              )}
            </dd>
            <dt>From</dt>
            <dd>{s.discovered ? "A folder in a root" : "Listed in keep.yml"}</dd>
          </dl>
        </section>
      )}

      <section class="section">
        <SectionHead index={2} title="Size over time" />
        {history && history.length === 0 && <Empty>No good backup yet.</Empty>}
        {history && history.length > 0 && (
          <>
            <div
              class="bars"
              role="img"
              aria-label={`Snapshot sizes over the last ${history.length} backups`}
            >
              {history.map((h) => (
                <div
                  key={h.run_id}
                  class="bar"
                  style={{ height: `${Math.max(2, (h.size / max) * 100)}%` }}
                  title={`Run ${h.run_id}: ${bytes(h.size)}, ${ago(h.finished)}`}
                />
              ))}
            </div>
            <p class="muted">
              Last {plural(history.length, "good backup")}, up to {bytes(max)}.
            </p>
          </>
        )}
      </section>

      {history && history.length > 0 && (
        <section class="section">
          <SectionHead index={3} title="Recent backups" />
          <div class="list">
            {[...history]
              .reverse()
              .slice(0, 15)
              .map((h) => (
                <div class="list-row" key={h.run_id}>
                  <Dot tone={runTone(h.status)} title={RUN_LABEL[h.status]} />
                  <a class="list-main list-link" href={`/runs/${h.run_id}`}>
                    <span class="list-title">
                      Run {h.run_id} <span class="muted">· {ago(h.finished)}</span>
                    </span>
                    <span class="list-sub">
                      {h.message ||
                        `${bytes(h.size)} in ${plural(h.files, "file")} · ${took(h.started, h.finished)}`}
                    </span>
                  </a>
                </div>
              ))}
          </div>
        </section>
      )}
    </div>
  );
}
