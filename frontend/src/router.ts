import { useEffect, useState } from "preact/hooks";

/**
 * Path routes (the server answers every non-API path with the app):
 *   /                    sources and the last run
 *   /runs                every run
 *   /runs/:id            one run: its sources and log
 *   /sources/:name       one source: size over time, recent runs
 *   /restores            restore folders
 *   /restores/:name      one restore: browse and download
 *   /settings            schedule, retention, watched folders
 */
export type Route =
  | { page: "home" }
  | { page: "runs" }
  | { page: "run"; id: number }
  | { page: "source"; name: string }
  | { page: "restores" }
  | { page: "restore"; name: string }
  | { page: "settings" };

export function parseRoute(path: string): Route {
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  switch (parts[0]) {
    case "runs": {
      const id = Number(parts[1]);
      return parts[1] && Number.isInteger(id) && id > 0 ? { page: "run", id } : { page: "runs" };
    }
    case "sources":
      if (parts[1]) return { page: "source", name: parts[1] };
      break;
    case "restores":
      return parts[1] ? { page: "restore", name: parts[1] } : { page: "restores" };
    case "settings":
    case "config": // the old address
      return { page: "settings" };
  }
  return { page: "home" };
}
const listeners = new Set<() => void>();

export function navigate(url: string, replace = false) {
  if (url === window.location.pathname) return;
  if (replace) history.replaceState(null, "", url);
  else history.pushState(null, "", url);
  window.scrollTo(0, 0);
  listeners.forEach((fn) => fn());
}

/** Lets plain <a href="/…"> links navigate without a page load. */
export function onLinkClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
    return;
  }
  const a = (e.target as Element).closest("a");
  if (
    !a ||
    a.target ||
    a.hasAttribute("download") ||
    a.origin !== window.location.origin ||
    a.pathname.startsWith("/api/")
  ) {
    return;
  }
  e.preventDefault();
  navigate(a.pathname);
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.pathname));
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.pathname));
    listeners.add(update);
    window.addEventListener("popstate", update);
    return () => {
      listeners.delete(update);
      window.removeEventListener("popstate", update);
    };
  }, []);
  return route;
}
