import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { ago, bytes, KIND_LABEL, plural, RUN_LABEL, runTone, took } from "../lib";
import type { LogLine, Run } from "../types";
import { Dot, Empty, ErrorNote, Figure, SectionHead } from "./ui";

/** Every run, newest first. */
export function RunsPage() {
  const { data: first, error } = useData(() => api.runs(), 10_000);
  const [more, setMore] = useState<Run[]>([]);
  const [done, setDone] = useState(false);
  const runs = [...(first ?? []), ...more];

  const loadMore = async () => {
    const page = await api.runs(runs[runs.length - 1].id);
    setMore([...more, ...page]);
    if (page.length < 50) setDone(true);
  };

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">History</div>
        <h1 class="page-title">Runs</h1>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      <section class="section">
        <SectionHead index={1} title="All runs" />
        {!first && !error && <div class="loading loading-list" />}
        {first && runs.length === 0 && <Empty>No runs yet.</Empty>}
        {runs.length > 0 && (
          <div class="list">
            {runs.map((r) => (
              <RunRow key={r.id} r={r} />
            ))}
          </div>
        )}
        {first && first.length === 50 && !done && (
          <button class="btn btn-ghost btn-small" onClick={loadMore}>
            Older runs
          </button>
        )}
      </section>
    </div>
  );
}

function RunRow({ r }: { r: Run }) {
  return (
    <div class="list-row">
      <Dot tone={runTone(r.status)} title={RUN_LABEL[r.status]} />
      <a class="list-main list-link" href={`/runs/${r.id}`}>
        <span class="list-title">
          {KIND_LABEL[r.kind] ?? "Run"} {r.id} <span class="muted">· {ago(r.started)}</span>
        </span>
        <span class="list-sub">{r.summary || RUN_LABEL[r.status]}</span>
      </a>
      <span class="muted">{r.trigger}</span>
    </div>
  );
}

/** One run: its sources and its log, followed live while it runs. */
export function RunPage({ id }: { id: number }) {
  const { data: run, error, reload } = useData(() => api.run(id), 0, [id]);
  const [log, setLog] = useState<LogLine[]>([]);
  const last = useRef(0);

  useEffect(() => {
    setLog([]);
    last.current = 0;
    let stop = false;
    const poll = async () => {
      try {
        const lines = await api.runLog(id, last.current);
        if (stop) return;
        if (lines.length) {
          last.current = lines[lines.length - 1].id;
          setLog((l) => [...l, ...lines]);
        }
      } catch {
        // shown by the run's own error
      }
    };
    poll();
    const timer = setInterval(() => {
      poll();
      reload();
    }, 2_000);
    return () => {
      stop = true;
      clearInterval(timer);
    };
  }, [id]);

  const running = run?.status === "running";
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">
          <a class="link" href="/runs">
            Runs
          </a>
        </div>
        <h1 class="page-title">
          {run ? (KIND_LABEL[run.kind] ?? "Run") : "Run"} {id}
        </h1>
        {run && (
          <>
            <div class="figures stagger">
              <Figure value={RUN_LABEL[run.status]} label="Status" tone={runTone(run.status)} />
              {run.kind === "backup" && (
                <Figure value={bytes(run.size)} label={`In ${plural(run.files, "file")}`} />
              )}
              <Figure
                value={
                  running
                    ? took(run.started, new Date().toISOString())
                    : took(run.started, run.finished)
                }
                label={`Started ${ago(run.started)} · ${run.trigger}`}
              />
            </div>
            {run.summary && <p class="muted page-lede">{run.summary}</p>}
            {run.kind === "restore" && run.status !== "running" && (
              <p>
                <a class="link" href="/restores">
                  Open Restores
                </a>
              </p>
            )}
          </>
        )}
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}

      {run?.sources && run.sources.length > 0 && (
        <section class="section">
          <SectionHead index={1} title="Sources" />
          <div class="list">
            {run.sources.map((s) => (
              <div class="list-row" key={s.name}>
                <Dot tone={runTone(s.status)} title={RUN_LABEL[s.status]} />
                <a class="list-main list-link" href={`/sources/${encodeURIComponent(s.name)}`}>
                  <span class="list-title">{s.name}</span>
                  <span
                    class={`list-sub ${s.status === "failed" ? "tone-bad" : s.status === "warn" ? "tone-warn" : ""}`}
                  >
                    {s.message ||
                      [
                        s.strategy,
                        s.databases ? plural(s.databases, "database") : "",
                        s.status === "running" ? "running" : bytes(s.size),
                        s.finished && s.started ? took(s.started, s.finished) : "",
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                  </span>
                  {s.local_error && s.local_error !== "not prepared" && (
                    <span class="list-sub tone-warn">Local copy: {s.local_error}</span>
                  )}
                </a>
              </div>
            ))}
          </div>
        </section>
      )}

      <section class="section">
        <SectionHead index={2} title="Log">
          {running && <span class="chip chip-accent">Live</span>}
        </SectionHead>
        {log.length === 0 ? (
          <Empty>No log lines{running ? " yet" : ""}.</Empty>
        ) : (
          <pre class="log">
            {log.map((l) => (
              <div key={l.id} class={`log-${l.level}`}>
                <span class="log-time">{new Date(l.at).toLocaleTimeString()}</span>
                {l.text}
              </div>
            ))}
          </pre>
        )}
      </section>
    </div>
  );
}
