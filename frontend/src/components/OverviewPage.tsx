import { Database, FolderPlus, HardDrive, Play, Plus } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { navigate } from "../router";
import { ago, bytes, plural, RUN_LABEL, runTone, STATE_LABEL, stateTone, until } from "../lib";
import type { Overview, SourceStatus, Suggestion } from "../types";
import { AddSourceDialog, type AddStart } from "./AddSource";
import { Dot, Empty, ErrorNote, Figure, SectionHead, useAction } from "./ui";

const RANK: Record<string, number> = { errors: 0, stale: 1, never: 2, running: 3, ok: 4 };

/** Sources with their state, the last run, and Run now. */
export function OverviewPage() {
  const { data: o, error, reload } = useData(api.overview, 5_000);
  const { data: suggestions, reload: reloadSuggestions } = useData(api.suggestions, 60_000);
  const [adding, setAdding] = useState<AddStart | null>(null);
  const action = useAction();

  const runNow = () =>
    action.run(async () => {
      const { id } = await api.runNow();
      await reload();
      if (id) navigate(`/runs/${id}`);
    });

  const sources = o ? [...o.sources].sort((a, b) => RANK[a.state] - RANK[b.state]) : [];
  const good = o?.sources.filter((s) => s.state === "ok" || s.state === "running").length ?? 0;

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Backups{o?.engine ? ` · ${o.engine}` : ""}</div>
        <h1 class="page-title">Keep</h1>
        <div class="figures stagger">
          <LastRunFigure o={o} />
          <Figure
            value={o ? good : "—"}
            unit={o ? `/${o.sources.length}` : ""}
            label="Sources backed up"
            tone={o && good < o.sources.length ? "warn" : ""}
          />
          <Figure value={o?.repo ? bytes(o.repo.size) : "—"} label="Repository" />
          <VerifyFigure o={o} />
          <Figure
            value={o?.running ? "Now" : o?.next ? until(o.next) : "—"}
            label={
              o?.next_kind === "verify"
                ? "Next: verify"
                : o?.every
                  ? `Next backup · every ${o.every}`
                  : "Next backup"
            }
          />
        </div>
        <div class="toolbar">
          <button class="btn btn-primary" onClick={runNow} disabled={action.busy || !!o?.running}>
            <Play size={14} />{" "}
            {!o?.running
              ? "Run now"
              : o.kind === "backup"
                ? `Backing up ${o.current ?? ""}`
                : `Busy: ${o.current ?? ""}`}
          </button>
          {o?.last_run && (
            <a class="link" href={`/runs/${o.last_run.id}`}>
              Run {o.last_run.id}: {o.last_run.summary || RUN_LABEL[o.last_run.status]}
            </a>
          )}
        </div>
      </header>

      {error && <ErrorNote>{error}</ErrorNote>}
      {action.error && <ErrorNote>{action.error}</ErrorNote>}
      {o?.config_error && (
        <ErrorNote>
          <strong>Settings:</strong> {o.config_error}. Runs fail until it's fixed in{" "}
          <a class="link" href="/settings">
            Settings
          </a>
          .
        </ErrorNote>
      )}

      <section class="section">
        <SectionHead index={1} title="Sources">
          {o && <span class="muted">{plural(o.sources.length, "source")}</span>}
          <button class="btn btn-primary btn-small" onClick={() => setAdding({})}>
            <Plus size={14} /> Add
          </button>
        </SectionHead>
        {!o && !error && <div class="loading loading-list" />}
        {o && o.sources.length === 0 && !o.config_error && (
          <Empty>Nothing to back up yet. Add a folder, or one of the suggestions below.</Empty>
        )}
        {sources.length > 0 && (
          <div class="list">
            {sources.map((s) => (
              <SourceRow key={s.name} s={s} current={o?.current} />
            ))}
          </div>
        )}
      </section>

      {suggestions && suggestions.length > 0 && (
        <section class="section">
          <SectionHead index={2} title="Not backed up">
            <span class="muted">what containers keep that no source covers</span>
          </SectionHead>
          <div class="list">
            {suggestions.map((sg) => (
              <SuggestionRow
                key={sg.container + (sg.path ?? sg.volume)}
                sg={sg}
                onAdd={() =>
                  setAdding({
                    path: sg.path,
                    strategy: sg.strategy,
                    container: sg.container,
                    volume: sg.volume,
                  })
                }
              />
            ))}
          </div>
        </section>
      )}

      {adding && (
        <AddSourceDialog
          start={adding}
          onClose={() => setAdding(null)}
          onSaved={() => {
            setAdding(null);
            reload();
            reloadSuggestions();
          }}
        />
      )}
    </div>
  );
}

function SuggestionRow({ sg, onAdd }: { sg: Suggestion; onAdd: () => void }) {
  const what =
    sg.kind === "folder"
      ? sg.path
      : sg.kind === "database"
        ? `${sg.strategy} database in volume ${sg.volume}`
        : `volume ${sg.volume}`;
  return (
    <div class="list-row">
      <span class="suggest-icon" aria-hidden="true">
        {sg.kind === "folder" ? (
          <FolderPlus size={15} />
        ) : sg.kind === "database" ? (
          <Database size={15} />
        ) : (
          <HardDrive size={15} />
        )}
      </span>
      <div class="list-main suggest-main">
        <span class="list-title">
          {sg.container} {!sg.running && <span class="muted">· stopped</span>}
        </span>
        <span class="list-sub mono">{what}</span>
        {sg.kind === "volume" && (
          <span class="list-sub">
            A Docker volume Keep can't reach. Dump it if it's a database, or move it to a folder.
          </span>
        )}
      </div>
      {sg.kind !== "volume" && (
        <button class="btn btn-ghost btn-small" onClick={onAdd}>
          <Plus size={14} /> Add
        </button>
      )}
    </div>
  );
}

/** The last repository check. */
function VerifyFigure({ o }: { o: Overview | null }) {
  const v = o?.last_verify;
  if (o?.running && o.kind === "verify") {
    return <Figure value="Running" label="Last verify" tone="accent" />;
  }
  if (!v) return <Figure value="—" label="Last verify" />;
  return (
    <a class="figure-link" href={`/runs/${v.id}`}>
      <Figure
        value={ago(v.started)}
        label={`Last verify · ${RUN_LABEL[v.status]}`}
        tone={runTone(v.status)}
        title={v.summary}
      />
    </a>
  );
}

function LastRunFigure({ o }: { o: Overview | null }) {
  if (!o) return <Figure value="—" label="Last backup" />;
  if (o.running && o.kind === "backup")
    return <Figure value="Running" label="Last backup" tone="accent" />;
  if (!o.last_run) return <Figure value="Never" label="Last backup" tone="warn" />;
  return (
    <Figure
      value={ago(o.last_run.started)}
      label={`Last backup · ${RUN_LABEL[o.last_run.status]}`}
      tone={runTone(o.last_run.status)}
    />
  );
}

function SourceRow({ s, current }: { s: SourceStatus; current?: string }) {
  const where = s.host_path ?? s.path ?? (s.container ? `${s.container} container` : "");
  const facts = [
    s.strategy,
    s.success?.databases ? plural(s.success.databases, "database") : "",
    s.success ? bytes(s.success.size) : "",
    s.partial ? "partial" : "",
  ].filter(Boolean);
  let note = "";
  if (s.state === "running" || current === s.name) note = "Backing up now";
  else if (s.state === "errors") note = s.latest?.message || "The last backup failed";
  else if (s.success) note = `Backed up ${ago(s.success.finished)}`;
  else note = "Never backed up";
  return (
    <div class="list-row">
      <Dot tone={stateTone(s.state)} title={STATE_LABEL[s.state]} />
      <a class="list-main list-link" href={`/sources/${encodeURIComponent(s.name)}`}>
        <span class="list-title">
          {s.name} <span class="muted source-where">{where}</span>
        </span>
        <span class={`list-sub ${s.state === "errors" ? "tone-bad" : ""}`}>{note}</span>
        <span class="list-sub">{facts.join(" · ")}</span>
      </a>
      <span class={`chip ${chipClass(s.state)}`}>{STATE_LABEL[s.state]}</span>
    </div>
  );
}

function chipClass(state: string) {
  switch (state) {
    case "errors":
      return "chip-bad";
    case "stale":
    case "never":
      return "chip-warn";
    case "running":
      return "chip-accent";
  }
  return "";
}
