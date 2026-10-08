import { ArrowLeft, LogOut } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api, setUnauthorizedHandler } from "./api";
import { ConfigPage } from "./components/ConfigPage";
import { Login } from "./components/Login";
import { OverviewPage } from "./components/OverviewPage";
import { RunPage, RunsPage } from "./components/RunsPage";
import { SourcePage } from "./components/SourcePage";
import { onLinkClick, useRoute, type Route } from "./router";
import type { Session } from "./types";

export function App() {
  const route = useRoute();
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    setUnauthorizedHandler(() => setSession({ authenticated: false }));
    api
      .session()
      .then(setSession)
      .catch((e: Error) => setError(e.message));
  }, []);

  if (error) return <div class="boot">Can't reach Keep: {error}</div>;
  if (!session) return <div class="boot" />;
  if (!session.authenticated) return <Login onDone={setSession} />;
  return (
    <Shell
      foyerURL={session.foyer_url}
      route={route}
      onSignOut={() => setSession({ authenticated: false })}
    />
  );
}

const NAV: { pages: Route["page"][]; href: string; label: string }[] = [
  { pages: ["home", "source"], href: "/", label: "Sources" },
  { pages: ["runs", "run"], href: "/runs", label: "Runs" },
  { pages: ["config"], href: "/config", label: "Config" },
];

function Shell(props: { route: Route; foyerURL?: string; onSignOut: () => void }) {
  const { route } = props;
  const signOut = async () => {
    await api.logout().catch(() => {});
    props.onSignOut();
  };
  return (
    <div class="shell" onClick={onLinkClick}>
      <header class="topbar">
        {props.foyerURL && (
          <a class="home-link" href={props.foyerURL} title="Back to Foyer">
            <ArrowLeft size={14} />
            <span class="home-link-text">Foyer</span>
          </a>
        )}
        <a class="brand" href="/">
          <span class="brand-mark" aria-hidden="true" />
          Keep
        </a>
        <span class="spacer" />
        <nav class="topnav" aria-label="Pages">
          {NAV.map((n) => (
            <a key={n.href} class={n.pages.includes(route.page) ? "active" : ""} href={n.href}>
              {n.label}
            </a>
          ))}
        </nav>
        <button class="icon-btn" onClick={signOut} title="Sign out" aria-label="Sign out">
          <LogOut size={16} />
        </button>
      </header>
      <main>
        {route.page === "home" && <OverviewPage />}
        {route.page === "runs" && <RunsPage />}
        {route.page === "run" && <RunPage id={route.id} />}
        {route.page === "source" && <SourcePage name={route.name} />}
        {route.page === "config" && <ConfigPage />}
      </main>
    </div>
  );
}
