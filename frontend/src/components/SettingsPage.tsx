import { FolderPlus, Trash2, Undo2 } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { lines, restoreSource, unwatchFolder, watchFolder } from "../configEdit";
import { useData, useUnsavedWarning } from "../hooks";
import type { Config, Retention } from "../types";
import { FolderPicker } from "./AddSource";
import { Dialog, Empty, ErrorNote, Field, SectionHead, useAction } from "./ui";

const RETENTION: [keyof Retention, string][] = [
  ["latest", "Latest"],
  ["hourly", "Hourly"],
  ["daily", "Daily"],
  ["weekly", "Weekly"],
  ["monthly", "Monthly"],
  ["annual", "Yearly"],
];

/** Schedule, retention, watched folders, skipped folders and global excludes. */
export function SettingsPage() {
  const { data, error, reload } = useData(api.config);
  const [cfg, setCfg] = useState<Config | null>(null);
  const [excludes, setExcludes] = useState("");
  const [picking, setPicking] = useState(false);
  const [saved, setSaved] = useState(false);
  const save = useAction();

  useEffect(() => {
    if (data) {
      setCfg(data.config);
      setExcludes(data.config.excludes.join("\n"));
    }
  }, [data]);

  const dirty =
    !!cfg &&
    !!data &&
    JSON.stringify({ ...cfg, excludes: lines(excludes) }) !== JSON.stringify(data.config);
  useUnsavedWarning(dirty);

  const submit = (e: Event) => {
    e.preventDefault();
    if (!cfg) return;
    save.run(async () => {
      await api.saveConfig({ ...cfg, excludes: lines(excludes) });
      await reload();
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    });
  };

  const skipped = cfg?.sources.filter((s) => s.skip && !s.path) ?? [];
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Setup</div>
        <h1 class="page-title">Settings</h1>
        <p class="muted page-lede">
          Changes apply from the next run. Folders are added and changed from{" "}
          <a class="link" href="/">
            Sources
          </a>
          .
        </p>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!cfg && !error && <div class="loading loading-list" />}
      {cfg && (
        <form class="page" onSubmit={submit}>
          <section class="section">
            <SectionHead index={1} title="Schedule" />
            <div class="form-grid">
              <Field label="Back up every" hint="Like 12h, 6h or 24h. Run now works any time.">
                <input
                  class="input"
                  value={cfg.every}
                  onInput={(e) => setCfg({ ...cfg, every: e.currentTarget.value })}
                />
              </Field>
              <Field
                label="Stale after"
                hint="A source with no good backup for this long is flagged."
              >
                <input
                  class="input"
                  value={cfg.stale_after}
                  onInput={(e) => setCfg({ ...cfg, stale_after: e.currentTarget.value })}
                />
              </Field>
            </div>
          </section>

          <section class="section">
            <SectionHead index={2} title="Snapshots kept" />
            <p class="muted">
              Per source, set on each of its paths in {cfg.engine.type}. The newest of each kind is
              kept.
            </p>
            <div class="form-grid retention">
              {RETENTION.map(([key, label]) => (
                <Field key={key} label={label}>
                  <input
                    class="input"
                    type="number"
                    min={key === "latest" ? 1 : 0}
                    value={cfg.retention[key]}
                    onInput={(e) =>
                      setCfg({
                        ...cfg,
                        retention: { ...cfg.retention, [key]: Number(e.currentTarget.value) || 0 },
                      })
                    }
                  />
                </Field>
              ))}
            </div>
          </section>

          <section class="section">
            <SectionHead index={3} title="Watched folders">
              <button
                type="button"
                class="btn btn-ghost btn-small"
                onClick={() => setPicking(true)}
              >
                <FolderPlus size={14} /> Watch a folder
              </button>
            </SectionHead>
            <p class="muted">
              Every folder inside a watched folder is a source, including ones added later.
            </p>
            {cfg.roots.length === 0 && <Empty>No watched folders.</Empty>}
            <div class="list">
              {cfg.roots.map((r) => (
                <div class="list-row" key={r.path}>
                  <div class="list-main">
                    <span class="list-title mono">{r.path}</span>
                    {!!r.skip?.length && (
                      <span class="list-sub">Not these: {r.skip.join(", ")}</span>
                    )}
                  </div>
                  <button
                    type="button"
                    class="icon-btn"
                    title="Stop watching"
                    aria-label={`Stop watching ${r.path}`}
                    onClick={() => setCfg(unwatchFolder(cfg, r.path))}
                  >
                    <Trash2 size={15} />
                  </button>
                </div>
              ))}
            </div>
            {skipped.length > 0 && (
              <>
                <h3 class="eyebrow">Skipped</h3>
                <div class="list">
                  {skipped.map((s) => (
                    <div class="list-row" key={s.name}>
                      <div class="list-main">
                        <span class="list-title">{s.name}</span>
                        <span class="list-sub">Found in a watched folder, not backed up</span>
                      </div>
                      <button
                        type="button"
                        class="btn btn-ghost btn-small"
                        onClick={() => setCfg(restoreSource(cfg, s.name))}
                      >
                        <Undo2 size={13} /> Back up again
                      </button>
                    </div>
                  ))}
                </div>
              </>
            )}
          </section>

          <section class="section">
            <SectionHead index={4} title="Left out everywhere" />
            <Field
              label="Patterns"
              hint="One per line, on top of each source's own: /x from the top of a source, name at any depth, dir/ for folders only."
            >
              <textarea
                class="input"
                rows={4}
                value={excludes}
                onInput={(e) => setExcludes(e.currentTarget.value)}
              />
            </Field>
          </section>

          <section class="section">
            <SectionHead index={5} title="Engine" />
            <div class="form-grid">
              <Field
                label={`${cfg.engine.type} container`}
                hint="Keep runs the engine's CLI in it."
              >
                <input
                  class="input"
                  value={cfg.engine.container}
                  onInput={(e) =>
                    setCfg({ ...cfg, engine: { ...cfg.engine, container: e.currentTarget.value } })
                  }
                />
              </Field>
              <Field
                label="Staging"
                hint="Database copies wait here; the engine must see it at the same path."
              >
                <input
                  class="input"
                  value={cfg.staging}
                  onInput={(e) => setCfg({ ...cfg, staging: e.currentTarget.value })}
                />
              </Field>
            </div>
          </section>

          <div class="toolbar save-bar">
            <button class="btn btn-primary" disabled={save.busy || !dirty}>
              Save
            </button>
            {saved && <span class="tone-good">Saved</span>}
            {dirty && <span class="muted">Unsaved changes</span>}
            {save.error && <span class="form-error">{save.error}</span>}
          </div>
        </form>
      )}

      {picking && cfg && (
        <Dialog title="Watch a folder" onClose={() => setPicking(false)} wide>
          <FolderPicker
            onChoose={(p) => {
              setCfg(watchFolder(cfg, p));
              setPicking(false);
            }}
          />
        </Dialog>
      )}
    </div>
  );
}
