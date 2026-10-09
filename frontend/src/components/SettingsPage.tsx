import { ArchiveRestore, FolderPlus, ShieldCheck, Trash2, Undo2 } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { lines, restoreSource, unwatchFolder, watchFolder } from "../configEdit";
import { useData, useUnsavedWarning } from "../hooks";
import { navigate } from "../router";
import { ago, bytes, plural } from "../lib";
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
  const verify = useAction();

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
            {cfg.ignored.length > 0 && (
              <>
                <h3 class="eyebrow">Not suggested</h3>
                <div class="list">
                  {cfg.ignored.map((p) => (
                    <div class="list-row" key={p}>
                      <div class="list-main">
                        <span class="list-title mono">{p}</span>
                        <span class="list-sub">
                          Left out on purpose: not in Not backed up or Foyer
                        </span>
                      </div>
                      <button
                        type="button"
                        class="btn btn-ghost btn-small"
                        onClick={() =>
                          setCfg({ ...cfg, ignored: cfg.ignored.filter((x) => x !== p) })
                        }
                      >
                        <Undo2 size={13} /> Suggest again
                      </button>
                    </div>
                  ))}
                </div>
              </>
            )}
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
            <SectionHead index={5} title="Verify">
              <button
                type="button"
                class="btn btn-ghost btn-small"
                disabled={verify.busy}
                onClick={() =>
                  verify.run(async () => {
                    const { id } = await api.runNow("verify");
                    if (id) navigate(`/runs/${id}`);
                  })
                }
              >
                <ShieldCheck size={14} /> Verify now
              </button>
            </SectionHead>
            <p class="muted">
              Checks the repository's structure and reads a sample of the files back, as a job of
              its own. It also refreshes the list of snapshots that restores and old snapshots read.
            </p>
            {verify.error && <ErrorNote>{verify.error}</ErrorNote>}
            <div class="form-grid">
              <Field label="Verify every" hint="Like 168h for a week.">
                <input
                  class="input"
                  value={cfg.verify.every}
                  onInput={(e) =>
                    setCfg({ ...cfg, verify: { ...cfg.verify, every: e.currentTarget.value } })
                  }
                />
              </Field>
              <Field label="Files read back (%)" hint="Each one is downloaded from the repository.">
                <input
                  class="input"
                  type="number"
                  min={1}
                  max={100}
                  value={cfg.verify.percent}
                  onInput={(e) =>
                    setCfg({
                      ...cfg,
                      verify: { ...cfg.verify, percent: Number(e.currentTarget.value) || 0 },
                    })
                  }
                />
              </Field>
              <Field
                label="Heartbeat URL"
                hint="Pinged at the start and end of each verify (/fail when it finds problems), like the backup's."
                class="span-2"
              >
                <input
                  class="input mono"
                  placeholder="https://lookout…/ping/…"
                  value={cfg.verify.heartbeat ?? ""}
                  onInput={(e) =>
                    setCfg({ ...cfg, verify: { ...cfg.verify, heartbeat: e.currentTarget.value } })
                  }
                />
              </Field>
            </div>
          </section>

          <section class="section">
            <SectionHead index={6} title="Local copy" />
            <p class="muted">
              A second repository in a folder on this server, on another disk than the data. Every
              backup snapshots each source into it too, straight from the disk, so it doesn't depend
              on the main repository or the network, and restores come from it when they can (much
              faster). Keep creates it in an empty folder, with the engine's password.
            </p>
            <div class="form-grid">
              <Field
                label="Folder"
                hint="Empty turns it off. The engine must see it at the same path, read-write, and it can't be in anything backed up."
                class="span-2"
              >
                <input
                  class="input mono"
                  placeholder="/mnt/hdd/keep-repo"
                  value={cfg.local.path ?? ""}
                  onInput={(e) =>
                    setCfg({ ...cfg, local: { ...cfg.local, path: e.currentTarget.value } })
                  }
                />
              </Field>
              <Field
                label="Heartbeat URL"
                hint="Pinged after each backup with how the local copy went (/fail when a source is missing from it)."
                class="span-2"
              >
                <input
                  class="input mono"
                  placeholder="https://lookout…/ping/…"
                  value={cfg.local.heartbeat ?? ""}
                  onInput={(e) =>
                    setCfg({ ...cfg, local: { ...cfg.local, heartbeat: e.currentTarget.value } })
                  }
                />
              </Field>
            </div>
          </section>

          <OldSnapshots cfg={cfg} setCfg={setCfg} />

          <section class="section">
            <SectionHead index={8} title="Engine" />
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
              <Field
                label="Restores"
                hint="Restores are written here; the engine must see it at the same path, read-write."
              >
                <input
                  class="input"
                  value={cfg.restores}
                  onInput={(e) => setCfg({ ...cfg, restores: e.currentTarget.value })}
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

/**
 * Snapshots of paths Keep doesn't back up now (from before Keep, or of
 * removed sources). Nothing expires them, so each can get a date after
 * which the weekly verify deletes them. Saved with the rest of the form.
 */
function OldSnapshots(props: { cfg: Config; setCfg: (c: Config) => void }) {
  const { cfg, setCfg } = props;
  const { data, error } = useData(api.repository);
  const restore = useAction();
  const dateFor = (p: string) => cfg.retire.find((r) => r.path === p)?.after ?? "";
  const setDate = (p: string, after: string) =>
    setCfg({
      ...cfg,
      retire: after
        ? [...cfg.retire.filter((r) => r.path !== p), { path: p, after }]
        : cfg.retire.filter((r) => r.path !== p),
    });
  const listed = data?.listed && !data.listed.startsWith("0001") ? data.listed : "";
  return (
    <section class="section">
      <SectionHead index={7} title="Old snapshots" />
      <p class="muted">
        Snapshots in the repository of folders Keep doesn't back up now: from before Keep, or of
        sources since removed. Nothing expires them; give one a date and the first verify on or
        after it deletes them all.
        {listed ? ` As of the last verify, ${ago(listed)}.` : " Listed by the first verify."}
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      {restore.error && <ErrorNote>{restore.error}</ErrorNote>}
      {data && data.others.length === 0 && listed && <Empty>None: Keep manages them all.</Empty>}
      {data && data.others.length > 0 && (
        <div class="list">
          {data.others.map((o) => (
            <div class="list-row old-row" key={o.path}>
              <div class="list-main">
                <span class="list-title mono">{o.path}</span>
                <span class="list-sub">
                  {plural(o.snapshots, "snapshot")}, {ago(o.oldest)} to {ago(o.newest)} · newest{" "}
                  {bytes(o.size)} in {plural(o.files, "file")}
                </span>
              </div>
              <label class="old-date">
                <span class="field-label">Delete after</span>
                <input
                  class="input"
                  type="date"
                  value={dateFor(o.path)}
                  onInput={(e) => setDate(o.path, e.currentTarget.value)}
                />
              </label>
              <button
                type="button"
                class="icon-btn"
                title="Restore the newest"
                aria-label={`Restore the newest snapshot of ${o.path}`}
                disabled={restore.busy}
                onClick={() =>
                  restore.run(async () => {
                    const { id } = await api.startRestore({ snapshot: o.latest });
                    navigate(`/runs/${id}`);
                  })
                }
              >
                <ArchiveRestore size={15} />
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
