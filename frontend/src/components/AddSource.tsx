import { ChevronRight, CornerLeftUp, Folder as FolderIcon } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { addSource, freeName, lines, nameFor, watchFolder } from "../configEdit";
import { useData } from "../hooks";
import type { Config, Listing, SourceConfig } from "../types";
import { Dialog, Field, useAction } from "./ui";

/** What the Add dialog starts from: nothing (browse), or a suggestion. */
export interface AddStart {
  path?: string;
  strategy?: string;
  container?: string;
  volume?: string;
}

/**
 * Adds a source: pick a folder (or take a suggested database), then choose
 * whether it's one source or a watched folder whose folders are each one.
 */
export function AddSourceDialog(props: {
  start: AddStart;
  onClose: () => void;
  onSaved: () => void;
}) {
  const database = props.start.strategy === "postgres" || props.start.strategy === "mariadb";
  const [path, setPath] = useState(props.start.path ?? "");
  const [picking, setPicking] = useState(!database && !props.start.path);
  const [each, setEach] = useState(false);
  const [name, setName] = useState(
    database ? `${props.start.container}-db` : props.start.path ? nameFor(props.start.path) : "",
  );
  const [strategy, setStrategy] = useState(props.start.strategy ?? "sqlite");
  const [container, setContainer] = useState(props.start.container ?? "");
  const [excludes, setExcludes] = useState("");
  const { busy, error, run } = useAction();

  const choose = (p: string) => {
    setPath(p);
    setName(nameFor(p));
    setPicking(false);
  };

  const save = (e: Event) => {
    e.preventDefault();
    run(async () => {
      const { config } = await api.config();
      let next: Config;
      if (each) {
        next = watchFolder(config, path);
      } else {
        const src: SourceConfig = {
          name: freeName(config, [], name.trim()),
          strategy,
          excludes: lines(excludes),
        };
        if (!database) src.path = path;
        if (database || strategy === "stop") src.container = container.trim();
        if (database) src.volume = props.start.volume;
        next = addSource(config, src);
      }
      await api.saveConfig(next);
      props.onSaved();
    });
  };

  if (picking) {
    return (
      <Dialog title="Choose a folder" onClose={props.onClose} wide>
        <FolderPicker start={path} onChoose={choose} />
      </Dialog>
    );
  }
  return (
    <Dialog
      title={database ? "Back up a database" : "Back up a folder"}
      onClose={props.onClose}
      footer={
        <>
          <span class="spacer" />
          <button class="btn btn-ghost" onClick={props.onClose}>
            Cancel
          </button>
          <button
            class="btn btn-primary"
            form="add-form"
            disabled={busy || (!each && !name.trim())}
          >
            Add
          </button>
        </>
      }
    >
      <form id="add-form" class="form" onSubmit={save}>
        {database ? (
          <p class="muted">
            Keep dumps the database inside <code>{props.start.container}</code> with{" "}
            {props.start.strategy === "postgres" ? "pg_dumpall" : "mariadb-dump"}, using the
            credentials it was started with. The dump covers the volume{" "}
            <code>{props.start.volume}</code>.
          </p>
        ) : (
          <Field label="Folder">
            <div class="picked">
              <code>{path}</code>
              <button
                type="button"
                class="btn btn-ghost btn-small"
                onClick={() => setPicking(true)}
              >
                Change
              </button>
            </div>
          </Field>
        )}
        {!database && (
          <div class="seg" role="radiogroup" aria-label="How to back it up">
            <button
              type="button"
              class={!each ? "active" : ""}
              aria-pressed={!each}
              onClick={() => setEach(false)}
            >
              One source
            </button>
            <button
              type="button"
              class={each ? "active" : ""}
              aria-pressed={each}
              onClick={() => setEach(true)}
            >
              Each folder inside
            </button>
          </div>
        )}
        {each ? (
          <p class="muted">
            Every folder inside becomes its own source, named after it, including folders added
            later. Change or skip them one by one afterwards.
          </p>
        ) : (
          <>
            <Field label="Name">
              <input class="input" value={name} onInput={(e) => setName(e.currentTarget.value)} />
            </Field>
            {!database && (
              <Field
                label="Strategy"
                hint={
                  strategy === "stop"
                    ? "For apps with no safe copy: their containers stop during the snapshot."
                    : strategy === "sqlite"
                      ? "SQLite databases inside are copied consistently while the app runs."
                      : "Plain files, taken as they are."
                }
              >
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
              <Field
                label={database ? "Container" : "Containers to stop"}
                hint={database ? "" : "Comma-separated."}
              >
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
                  rows={3}
                  value={excludes}
                  onInput={(e) => setExcludes(e.currentTarget.value)}
                />
              </Field>
            )}
          </>
        )}
        {error && <div class="form-error">{error}</div>}
      </form>
    </Dialog>
  );
}

/** Browses the folders Keep can see, showing what already backs each up. */
export function FolderPicker(props: { start?: string; onChoose: (path: string) => void }) {
  const [path, setPath] = useState(props.start ?? "");
  const { data: l, error } = useData<Listing>(() => api.browse(path), 0, [path]);
  return (
    <div class="picker">
      {l && l.roots.length > 1 && (
        <div class="chips">
          {l.roots.map((r) => (
            <button
              key={r}
              type="button"
              class={`chip chip-button ${l.path.startsWith(r) ? "chip-accent" : ""}`}
              onClick={() => setPath(r)}
            >
              {r}
            </button>
          ))}
        </div>
      )}
      {error && <div class="form-error">{error}</div>}
      {l && (
        <>
          <div class="picker-here">
            {l.parent && (
              <button
                type="button"
                class="icon-btn"
                onClick={() => setPath(l.parent!)}
                aria-label="Up a folder"
                title="Up"
              >
                <CornerLeftUp size={15} />
              </button>
            )}
            <code class="picker-path">{l.path}</code>
            <span class="spacer" />
            <button
              type="button"
              class="btn btn-primary btn-small"
              onClick={() => props.onChoose(l.path)}
            >
              Choose this folder
            </button>
          </div>
          {coverText(l) && <p class="muted picker-note">This folder: {coverText(l)}</p>}
          <div class="list picker-list">
            {l.folders.length === 0 && <div class="empty">No folders inside.</div>}
            {l.folders.map((f) => (
              <button
                type="button"
                key={f.path}
                class="list-row list-link picker-row"
                onClick={() => setPath(f.path)}
              >
                <FolderIcon size={15} />
                <span class="list-main">
                  <span class="list-title">{f.name}</span>
                  {coverText(f) && <span class="list-sub">{coverText(f)}</span>}
                </span>
                <ChevronRight size={15} />
              </button>
            ))}
          </div>
        </>
      )}
      {!l && !error && <div class="loading loading-list" />}
    </div>
  );
}

function coverText(c: {
  source?: string;
  excluded?: boolean;
  root?: boolean;
  contains?: number;
}): string {
  if (c.root) return "watched: each folder inside is a source";
  if (c.source && c.excluded) return `left out of ${c.source}`;
  if (c.source) return `backed up (${c.source})`;
  if (c.contains) return `has ${c.contains} source${c.contains === 1 ? "" : "s"} inside`;
  return "";
}
