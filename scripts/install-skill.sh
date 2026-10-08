#!/usr/bin/env bash
# Symlinks the /new-homelab-app skill into ~/.claude/skills, so it stays in
# step with this repo (git pull updates it).
set -euo pipefail
src=$(cd "$(dirname "$0")/../skill/new-homelab-app" && pwd)
dest=$HOME/.claude/skills/new-homelab-app
mkdir -p "$HOME/.claude/skills"
if [ -e "$dest" ] && [ ! -L "$dest" ]; then
  echo "$dest exists and isn't a symlink; move it away first" >&2
  exit 1
fi
ln -sfn "$src" "$dest"
echo "linked $dest -> $src"
