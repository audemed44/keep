import { Pencil, Trash2 } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { lines, removeSource, setSource } from "../configEdit";
import { navigate } from "../router";
import type { SourceStatus } from "../types";
import { useData } from "../hooks";
import { ago, bytes, plural, RUN_LABEL, runTone, STATE_LABEL, stateTone, took } from "../lib";
import { Dialog, Dot, Empty, ErrorNote, Field, Figure, SectionHead, useAction } from "./ui";

const STRATEGY: Record<string, string> = {
  sqlite: "Files, with every SQLite database copied consistently (VACUUM INTO)",
  files: "Plain files, nothing prepared",
  postgres: "pg_dumpall in the database's container",
  mariadb: "mariadb-dump in the database's container",
  stop: "Containers stopped for the snapshot, then started again",
};

/** One source: how it's backed up, its size over time, its recent runs. */
export function SourcePage({ name }: { name: string }) {
  const { data: o, error, reload } = useData(api.overview, 10_000);
  const [editing, setEditing] = useState(false);
  const removing = useAction();
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
          <SectionHead index={1} title="Setup">
            <button class="btn btn-ghost btn-small" onClick={() => setEditing(true)}>
              <Pencil size={13} /> Edit
            </button>
            <button
              class="btn btn-danger btn-small"
              disabled={removing.busy}
              onClick={() =>
                removing.run(async () => {
                  const what = s.discovered
                    ? `Stop backing up ${name}? It's skipped in its watched folder; the snapshots stay in the repository.`
                    : `Stop backing up ${name}? The snapshots stay in the repository.`;
                  if (!confirm(what)) return;
                  const { config } = await api.config();
                  await api.saveConfig(removeSource(config, name, s.discovered));
                  navigate("/");
                })
              }
            >
              <Trash2 size={13} /> Stop backing up
            </button>
          </SectionHead>
          {removing.error && <ErrorNote>{removing.error}</ErrorNote>}
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
            <dd>{s.discovered ? "A folder inside a watched folder" : "Added on its own"}</dd>
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
      {editing && s && (
        <EditSourceDialog
          s={s}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            reload();
          }}
        />
      )}
    </div>
  );
}

/** Changes how a source is backed up: strategy, containers, what's left out. */
function EditSourceDialog(props: { s: SourceStatus; onClose: () => void; onSaved: () => void }) {
  const { s } = props;
  const database = s.strategy === "postgres" || s.strategy === "mariadb";
  const [strategy, setStrategy] = useState(s.strategy);
  const [container, setContainer] = useState(s.container ?? "");
  const [excludes, setExcludes] = useState((s.excludes ?? []).join("\n"));
  const { busy, error, run } = useAction();
  const save = (e: Event) => {
    e.preventDefault();
    run(async () => {
      const { config } = await api.config();
      const own = config.sources.find((x) => x.name === s.name);
      await api.saveConfig(
        setSource(config, {
          ...own,
          name: s.name,
          path: s.discovered ? undefined : s.path,
          strategy,
          container: container.trim() || undefined,
          excludes: lines(excludes),
          skip: false,
        }),
      );
      props.onSaved();
    });
  };
  return (
    <Dialog
      title={`Edit ${s.name}`}
      onClose={props.onClose}
      footer={
        <>
          <span class="spacer" />
          <button class="btn btn-ghost" onClick={props.onClose}>
            Cancel
          </button>
          <button class="btn btn-primary" form="edit-form" disabled={busy}>
            Save
          </button>
        </>
      }
    >
      <form id="edit-form" class="form" onSubmit={save}>
        {!database && (
          <Field label="Strategy">
            <select
              class="input"
              value={strategy}
              onChange={(e) => setStrategy(e.currentTarget.value)}
            >
              <option value="sqlite">Files, with SQLite copies</option>
              <option value="files">Plain files</option>
              <option value="stop">Stop containers during the snapshot</option>
            </select>
          </Field>
        )}
        {(strategy === "stop" || database) && (
          <Field label={database ? "Container" : "Containers to stop"}>
            <input
              class="input"
              value={container}
              onInput={(e) => setContainer(e.currentTarget.value)}
            />
          </Field>
        )}
        {!database && (
          <Field
            label="Leave out"
            hint="One per line: /library from the top of the folder, *.log anywhere, cache/ for folders."
          >
            <textarea
              class="input"
              rows={4}
              value={excludes}
              onInput={(e) => setExcludes(e.currentTarget.value)}
            />
          </Field>
        )}
        {error && <div class="form-error">{error}</div>}
      </form>
    </Dialog>
  );
}
