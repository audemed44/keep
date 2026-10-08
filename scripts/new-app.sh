#!/usr/bin/env bash
# Turns a fresh copy of the template into a new app:
#   scripts/new-app.sh <name> "<Title>" <host port> "<one-line description>"
# e.g. scripts/new-app.sh pantry "Pantry" 8089 "What's in the cupboards"
#
# Replaces skeleton / Skeleton / SKELETON everywhere, moves cmd/skeleton,
# drops the template-only files, and checks nothing of the placeholder is left.
# Run it once, in the new repo's root, before the first commit.
set -euo pipefail

if [ $# -ne 4 ]; then
  sed -n '2,4p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi
name=$1 title=$2 port=$3 desc=$4
if ! [[ $name =~ ^[a-z][a-z0-9-]*$ ]]; then
  echo "name must be lowercase letters, digits and dashes: $name" >&2
  exit 2
fi
if ! [[ $port =~ ^[0-9]+$ ]]; then
  echo "port must be a number: $port" >&2
  exit 2
fi
upper=$(printf '%s' "$name" | tr 'a-z-' 'A-Z_')
cd "$(dirname "$0")/.."
[ -d cmd/skeleton ] || { echo "already renamed (no cmd/skeleton)" >&2; exit 1; }

# Template-only files.
rm -rf skill TEMPLATE.md scripts/install-skill.sh
sed -i '/scripts\/new-app.sh drops this line/d; /is_template/d' .github/workflows/docker.yml

git mv cmd/skeleton "cmd/$name" 2>/dev/null || mv cmd/skeleton "cmd/$name"

# Escape for sed replacement text.
esc() { printf '%s' "$1" | sed 's/[&/\]/\\&/g'; }
files=$(grep -rlI -e skeleton -e Skeleton -e SKELETON . \
  --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist --exclude=new-app.sh)
for f in $files; do
  sed -i \
    -e "s/TODO one line on what it does\./$(esc "$desc")./" \
    -e "s/TODO: one paragraph on what Skeleton is for\./$(esc "$desc")./" \
    -e "s/SKELETON/$upper/g" \
    -e "s/Skeleton/$(esc "$title")/g" \
    -e "s/skeleton/$name/g" \
    "$f"
done
sed -i "s/\"8089:8080\"/\"$port:8080\"/" docker-compose.example.yml README.md
sed -i '/TEMPLATE.md/,+1d' README.md
rm -f scripts/new-app.sh
rmdir scripts 2>/dev/null || true

left=$(grep -rnI -i skeleton . --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist || true)
if [ -n "$left" ]; then
  echo "placeholder left behind:" >&2
  echo "$left" >&2
  exit 1
fi
echo "Renamed to $title ($name, ${upper}_TOKEN, port $port). Next: go mod tidy, npm install, the checks in AGENTS.md."
