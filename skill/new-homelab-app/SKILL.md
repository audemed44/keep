---
name: new-homelab-app
description: Start a new self-hosted homelab app (Go + Preact + SQLite, like Lookout/Ledger/Parlor) from the audemed44/homelab-app-template repo, set up the GitHub repo the standard way, build the first version, and wire it into the homelab (main-stack compose, .env, Gatehouse host, Foyer card). Use when the user asks to build/create/start a new homelab app or service, or to turn a doc in aidev/ideas/ into an app.
---

# New homelab app

Every homelab app starts from **audemed44/homelab-app-template** (local clone:
`~/development/aidev/homelab-app-template`). Read its `TEMPLATE.md` first.
The template is a working app ("Skeleton") with an example `items` resource:
copy its patterns for the real features, then delete the example.

Run the steps in order. Steps 1–3 are setup and need no confirmation. Ask
before step 6 (deploy) unless the user already said to deploy.

## 0. Pin down the app

You need four things. Take them from the request or `aidev/ideas/<x>.md`.
Ask only for what's missing:

- **name**: lowercase, one word if possible (`parlor`, `ledger`). Offer 3–5
  names if the user hasn't picked one.
- **Title** for the UI (`Parlor`).
- **One-line description**.
- **Host port**: the next free one. Find it with
  `grep -oE '"[0-9]+:8080"' ~/homelab/main-stack/docker-compose.yml` plus
  `ss -ltn`. Apps so far: 8087 Ledger, 8088 Parlor, 8089 parlor-stream UDP.

## 1. Create the repo

```sh
export PATH=$HOME/.local/go/bin:$PATH
cd ~/development/aidev/homelab-app-template && git pull -q   # latest template
cd ~/development/aidev
gh repo create audemed44/<name> --public --template audemed44/homelab-app-template \
  --description "<one-line description>" --clone
cd <name>
scripts/new-app.sh <name> "<Title>" <port> "<one-line description>"
go mod tidy && (source ~/.nvm/nvm.sh && nvm use 22 >/dev/null && cd frontend && npm install)
```

Then commit to `main` as `chore: start <Title> from the homelab app template`
and push. This is the only commit that goes straight to main, because the
ruleset doesn't exist yet. If the template copy has already landed on main,
reset it with this commit.

## 2. Standard repo settings

```sh
gh repo edit audemed44/<name> --enable-rebase-merge --enable-merge-commit=false \
  --enable-squash-merge=false --delete-branch-on-merge --add-topic homelab
gh project link 1 --owner audemed44 --repo audemed44/<name>
gh api repos/audemed44/shelfloom/rulesets --jq '.[0].id' \
  | xargs -I{} gh api repos/audemed44/shelfloom/rulesets/{} \
  | jq 'del(.id, .node_id, ._links, .created_at, .updated_at, .source, .source_type, .current_user_can_bypass)' \
  > /tmp/ruleset.json
gh api -X POST repos/audemed44/<name>/rulesets --input /tmp/ruleset.json && rm /tmp/ruleset.json
```

The project only links the repo. Never add issues or PRs to it. There's no
Dependabot.

Workspace: add a row for the app to the projects table in
`~/development/aidev/AGENTS.md`, and write a `<name>-project` memory
(linked from MEMORY.md).

## 3. Build the first version

Work on a branch (`feat/<something>`) and open **one PR** for the whole first
version, with one commit per feature (Conventional Commits; CI checks them).

- Fill in the `TODO`s in `AGENTS.md` and `README.md`: what the app is, its
  packages, and its constraints. Keep the template's constraints: low memory,
  justified dependencies, token on every `/api/` call, dark only, works at
  phone width.
- Replace `items` everywhere: the store table and queries, the handlers,
  `api.ts`, `types.ts`, `ItemsPage.tsx`, the router and nav, and the tests.
  Add schema changes after the first release as `migrations` entries; never
  edit them.
- Make the Foyer widget (`internal/server/foyer.go`) show what matters for
  this app. Spec: `foyer/docs/app-widgets.md`.
- Swap `frontend/public/icon.svg` for the app's own mark: one shape in
  #2563ff on black.
- Never test with real secrets or the user's real data. Use fixtures and
  throwaway tokens.

Before pushing, run the checks from the app's `AGENTS.md`:

```sh
go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"
cd frontend && npm run format:check && npm run typecheck && npm test && npm run build
docker build -t <name>:dev .
```

Then run the binary with a throwaway token and data dir, add sample data
with curl, and take screenshots at 1280 and 390 wide, in dark mode, signed
in. Look at them before opening the PR. Put screenshots in the PR body or in
`docs/`. Clean up after the PR is raised: stop the processes and containers,
and remove `/tmp` data and images like `<name>:dev`.

PR body: what it does, how it was tested, the screenshots, and what the user
must do (for example, set a token). Don't add a "Generated with" footer.

## 4. Wire it into the homelab (after the user merges)

The image is `ghcr.io/audemed44/<name>:latest`, published by CI on merge.
Wait for the Docker workflow to go green: `gh run watch`.

All `~/homelab` edits go **straight to main**, without a PR (hoist-stacks
rule). Run `git status` there first, because Hoist commits too.

1. **Compose**: add the service to `~/homelab/main-stack/docker-compose.yml`
   after the last app, copying Parlor's or Ledger's block. Include
   `user: "1000:1000"`, `TZ`, `<NAME>_TOKEN`, `HOMEPAGE_URL`,
   `./<name>:/data`, `"<port>:8080"` and `mem_limit: 64m`. Add the keys
   (with blank values) to `main-stack/.env.example`.
2. **Token**: generate it with `openssl rand -hex 32` and **append** it to
   `main-stack/.env` (`echo '<NAME>_TOKEN=…' >> .env`). Read the file first.
   Never go through Hoist's env API: PUT replaces every key and once wiped
   the file. Don't print the token in chat. Tell the user it's in `.env`.
3. **Deploy**: commit with `feat(main-stack): add <Title>, <description>, on
   :<port>`, then push. Run
   `docker compose -p main-server pull <name> && docker compose -p main-server up -d <name>`
   from `main-stack/`. Check `docker ps` health and `curl -s localhost:<port>/healthz`.
4. **Gatehouse**: in `main-stack/gatehouse/gatehouse.json`, add a host next
   to Parlor's: `{"id": "<name>-aakash-shrivastava-com", "domains":
   ["<name>.aakash-shrivastava.com"], "upstream": "172.17.0.1:<port>",
   "enabled": true, "force_https": true}`. Back up the file first. Then
   reload: `docker kill -s HUP gatehouse`. Lookout picks up the new host on
   its own via discovery.
5. **Foyer**: add a service to `main-stack/foyer/foyer.yaml` in a fitting
   group, copying the Ledger/Parlor entries: `url: https://<name>.…`,
   `container`, `icon`, and an `app` widget at
   `http://<name>:8080/api/foyer/widget` with `key: ${<NAME>_TOKEN}`. Add
   `<NAME>_TOKEN` to Foyer's environment in compose, then
   `up -d foyer`. Back up the file first.
6. Open `https://<name>.aakash-shrivastava.com` and the Foyer card to check
   the wiring. Then update the project memory with the port, the URL, and
   what's deployed.

## Gotchas

- Fixes to a live app go through a PR, a merge, the published image, then
  `pull` and `up`. Never build a local image tagged as the GHCR one.
- After a reboot, check that Gatehouse came back (`docker start gatehouse`).
- Hoist's Deploy doesn't `git pull`. Deploy from the shell as above, or
  Pull then Deploy in Hoist.
- If something in the template turns out wrong or missing while building
  an app, fix it in the template too, with a separate PR to
  homelab-app-template, so the next app gets it.
