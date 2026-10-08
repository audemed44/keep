import { SectionHead } from "./ui";

export function AboutPage() {
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">About</div>
        <h1 class="page-title">Keep</h1>
        <p class="muted page-lede">Homelab backup orchestrator: consistent database dumps, Kopia snapshots, Lookout heartbeats.</p>
      </header>
      <section class="section">
        <SectionHead index={1} title="Foyer" />
        <p class="muted page-lede">
          Add Keep to Foyer as an <code>app</code> widget at{" "}
          <code>http://keep:8080/api/foyer/widget</code>, with the token as its key.
        </p>
      </section>
    </div>
  );
}
