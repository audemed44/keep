export interface Session {
  authenticated: boolean;
  /** Foyer, the homelab's start page (HOMEPAGE_URL). */
  foyer_url?: string;
}

/** The template's example record; replace it with the app's own. */
export interface Item {
  id: number;
  title: string;
  note: string;
  done: boolean;
  created: string;
}
