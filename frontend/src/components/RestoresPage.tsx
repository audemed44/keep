import { ArchiveRestore, Download, Folder, Trash2 } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { ago, bytes, plural, until } from "../lib";
import { navigate } from "../router";
import type { RestoreInfo, RestorePoint } from "../types";
import { CopyField, Dialog, Empty, ErrorNote, Field, Figure, SectionHead, useAction } from "./ui";

const when = (iso: string) =>
  new Date(iso).toLocaleString(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });

/** What a restore holds: a source (or one path in it) as of a date. */
function restoreTitle(r: RestoreInfo) {
  return r.path ? `${r.label}/${r.path}` : r.label;
}

/** Every restore folder, newest first. */
export function RestoresPage() {
  const { data, error, reload } = useData(api.restores, 10_000);
  const del = useAction();
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Copies out of the repository</div>
        <h1 class="page-title">Restores</h1>
        <p class="muted page-lede">
          A restore is written to a folder of its own, never over an app's files. Start one from a
          source's page. Moving files back into place is up to you. Restores are deleted after{" "}
          {data ? plural(data.keep_days, "day") : "a week"}.
        </p>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {del.error && <ErrorNote>{del.error}</ErrorNote>}
      <section class="section">
        <SectionHead index={1} title="Restores" />
        {!data && !error && <div class="loading loading-list" />}
        {data && data.restores.length === 0 && (
          <Empty>
            No restores. Open a{" "}
            <a class="link" href="/">
              source
            </a>{" "}
            and choose Restore.
          </Empty>
        )}
        {data && data.restores.length > 0 && (
          <div class="list">
            {data.restores.map((r) => (
              <div class="list-row" key={r.name}>
                <span class="suggest-icon" aria-hidden="true">
                  <ArchiveRestore size={15} />
                </span>
                <a class="list-main list-link" href={`/restores/${encodeURIComponent(r.name)}`}>
                  <span class="list-title">
                    {restoreTitle(r)} <span class="muted">· as of {when(r.taken)}</span>
                  </span>
                  <span class={`list-sub ${r.complete ? "" : "tone-warn"}`}>
                    {r.complete
                      ? `${bytes(r.size)} in ${plural(r.files, "file")}`
                      : `Incomplete: ${plural(r.files, "file")} so far`}{" "}
                    · deleted {until(r.expires)}
                  </span>
                </a>
                <button
                  class="icon-btn"
                  title="Delete"
                  aria-label={`Delete ${r.name}`}
                  disabled={del.busy}
                  onClick={() =>
                    del.run(async () => {
                      if (!confirm(`Delete the restore ${r.name}?`)) return;
                      await api.deleteRestore(r.name);
                      await reload();
                    })
                  }
                >
                  <Trash2 size={15} />
                </button>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

/** One restore: where it is, and its files to browse and download. */
export function RestorePage({ name }: { name: string }) {
  const { data: all, error } = useData(api.restores, 0, [name]);
  const [path, setPath] = useState("");
  const { data: listing, error: browseError } = useData(() => api.browseRestore(name, path), 0, [
    name,
    path,
  ]);
  const del = useAction();
  const r = all?.restores.find((x) => x.name === name);
  const crumbs = path ? path.split("/") : [];

  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">
          <a class="link" href="/restores">
            Restores
          </a>
        </div>
        <h1 class="page-title">{r ? restoreTitle(r) : name}</h1>
        {r && (
          <div class="figures stagger">
            <Figure value={when(r.taken)} label="Backup from" />
            <Figure value={bytes(r.size)} label={`In ${plural(r.files, "file")}`} />
            <Figure value={until(r.expires)} label="Deleted" />
          </div>
        )}
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {all && !r && <ErrorNote>No restore called {name}. It may have been deleted.</ErrorNote>}
      {r && !r.complete && (
        <ErrorNote>
          This restore didn't finish: see{" "}
          <a class="link" href={`/runs/${r.run_id}`}>
            restore {r.run_id}
          </a>
          .
        </ErrorNote>
      )}
      {r && (
        <section class="section">
          <SectionHead index={1} title="Where it is">
            <button
              class="btn btn-danger btn-small"
              disabled={del.busy}
              onClick={() =>
                del.run(async () => {
                  if (!confirm(`Delete the restore ${r.name}?`)) return;
                  await api.deleteRestore(r.name);
                  navigate("/restores");
                })
              }
            >
              <Trash2 size={13} /> Delete
            </button>
          </SectionHead>
          {del.error && <ErrorNote>{del.error}</ErrorNote>}
          <p class="muted">
            On the server, laid out like the source. Copy from here to put something back; Keep
            never writes into an app's folder.
          </p>
          <CopyField value={r.dir} label="Copy the folder's path" />
          <p class="muted">
            From{" "}
            <a class="link" href={`/runs/${r.run_id}`}>
              restore {r.run_id}
            </a>
            , made {ago(r.created)}.
          </p>
        </section>
      )}

      <section class="section">
        <SectionHead index={2} title="Files" />
        <nav class="crumbs" aria-label="Folder">
          <button class="link" onClick={() => setPath("")}>
            {name}
          </button>
          {crumbs.map((c, i) => (
            <span key={i}>
              {" / "}
              <button class="link" onClick={() => setPath(crumbs.slice(0, i + 1).join("/"))}>
                {c}
              </button>
            </span>
          ))}
        </nav>
        {browseError && <ErrorNote>{browseError}</ErrorNote>}
        {listing && listing.entries.length === 0 && <Empty>Nothing here.</Empty>}
        {listing && listing.entries.length > 0 && (
          <div class="list">
            {listing.entries.map((e) =>
              e.dir ? (
                <div class="list-row" key={e.path}>
                  <span class="suggest-icon" aria-hidden="true">
                    <Folder size={15} />
                  </span>
                  <button class="list-main list-link list-button" onClick={() => setPath(e.path)}>
                    <span class="list-title">{e.name}/</span>
                  </button>
                </div>
              ) : (
                <div class="list-row" key={e.path}>
                  <div class="list-main">
                    <span class="list-title mono">{e.name}</span>
                    <span class="list-sub">
                      {bytes(e.size)} · changed {when(e.modified)}
                    </span>
                  </div>
                  <a
                    class="icon-btn"
                    href={api.restoreFileURL(name, e.path)}
                    download={e.name}
                    title="Download"
                    aria-label={`Download ${e.name}`}
                  >
                    <Download size={15} />
                  </a>
                </div>
              ),
            )}
          </div>
        )}
      </section>
    </div>
  );
}

/** Starts a restore of a source: one of its backups, all of it or a path inside. */
export function RestoreDialog(props: { source: string; onClose: () => void }) {
  const { data, error } = useData(() => api.restorePoints(props.source), 0, [props.source]);
  const [run, setRun] = useState(0);
  const [whole, setWhole] = useState(true);
  const [path, setPath] = useState("");
  const start = useAction();
  const points: RestorePoint[] = data?.points ?? [];
  const chosen = run || points[0]?.run || 0;

  const submit = (e: Event) => {
    e.preventDefault();
    start.run(async () => {
      const { id } = await api.startRestore({
        source: props.source,
        run: chosen,
        path: whole ? "" : path.trim(),
      });
      navigate(`/runs/${id}`);
    });
  };
  return (
    <Dialog
      title={`Restore ${props.source}`}
      onClose={props.onClose}
      footer={
        <>
          <span class="spacer" />
          <button class="btn btn-ghost" onClick={props.onClose}>
            Cancel
          </button>
          <button
            class="btn btn-primary"
            form="restore-form"
            disabled={start.busy || !chosen || (!whole && !path.trim())}
          >
            Restore
          </button>
        </>
      }
    >
      <form id="restore-form" class="form" onSubmit={submit}>
        <p class="muted">
          Into a folder of its own under Restores, never over the app's files. Database copies come
          back in place of the live files they stand for.
        </p>
        {error && <div class="form-error">{error}</div>}
        {data && points.length === 0 && <Empty>No backup of {props.source} to restore yet.</Empty>}
        {points.length > 0 && (
          <Field
            label="Backup"
            hint={
              data?.listed && !data.listed.startsWith("0001")
                ? `Older backups as of the last verify, ${ago(data.listed)}; retention may have removed some since.`
                : "Older backups show up after the first verify."
            }
          >
            <select
              class="input"
              value={chosen}
              onChange={(e) => setRun(Number(e.currentTarget.value))}
            >
              {points.map((p) => (
                <option key={p.run} value={p.run}>
                  {when(p.taken)} · run {p.run} · {bytes(p.size)}
                </option>
              ))}
            </select>
          </Field>
        )}
        {points.length > 0 && (
          <fieldset class="choice">
            <label>
              <input type="radio" checked={whole} onChange={() => setWhole(true)} /> Everything
            </label>
            <label>
              <input type="radio" checked={!whole} onChange={() => setWhole(false)} /> One file or
              folder
            </label>
          </fieldset>
        )}
        {points.length > 0 && !whole && (
          <Field label="Path" hint="Inside the source, like data/app.db or uploads/2026.">
            <input
              class="input mono"
              value={path}
              onInput={(e) => setPath(e.currentTarget.value)}
              placeholder="data/app.db"
              autoFocus
            />
          </Field>
        )}
        {start.error && <div class="form-error">{start.error}</div>}
      </form>
    </Dialog>
  );
}
