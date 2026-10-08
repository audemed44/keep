import { api } from "../api";
import { useData } from "../hooks";
import { ErrorNote, SectionHead } from "./ui";

/** keep.yml as it is on disk, and how Keep is wired up. */
export function ConfigPage() {
  const { data: cfg, error } = useData(api.config);
  const { data: o } = useData(api.overview);
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Setup</div>
        <h1 class="page-title">Config</h1>
        <p class="muted page-lede">
          Keep reads <code>{cfg?.file ?? "keep.yml"}</code> again for every run, so edits apply
          without a restart. Edit it on the server.
        </p>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {o?.config_error && <ErrorNote>{o.config_error}</ErrorNote>}

      <section class="section">
        <SectionHead index={1} title="keep.yml" />
        {cfg ? (
          <pre class="log config-text">{cfg.text}</pre>
        ) : (
          !error && <div class="loading loading-list" />
        )}
      </section>

      <section class="section">
        <SectionHead index={2} title="Wiring" />
        <dl class="kv">
          <dt>Foyer</dt>
          <dd>
            A <code>keep</code> widget (or <code>app</code>) at <code>http://keep:8080</code>, with
            the token as its key. The card has Run now; <code>/api/foyer/backups</code> feeds the
            topology map.
          </dd>
          <dt>Lookout</dt>
          <dd>
            Set <code>KEEP_HEARTBEAT_URL</code> to a heartbeat's ping URL: Keep pings{" "}
            <code>/start</code> when a run begins and <code>/fail</code> when it fails.
          </dd>
          <dt>Engine</dt>
          <dd>
            {o?.engine ?? "kopia"}: Keep runs its CLI in its container. Keep and that container must
            see every source, and staging, at the same paths.
          </dd>
        </dl>
      </section>
    </div>
  );
}
