#!/usr/bin/env bash
#
# Fetch only the template for the chosen language from sample_devcontainer in
# taka1156/codespace-template (dist branch), then rewrite the "name" field of
# devcontainer.json with a project name entered interactively.
#
# Usage:
#   ./setup-devcontainer.sh              # pick a language interactively
#   ./setup-devcontainer.sh <language>   # specify the language (e.g. node)
#
# Run directly via curl (interactive input is read from /dev/tty, so it also
# works when stdin is a pipe):
#   curl -fsSL <raw URL> | bash
#   curl -fsSL <raw URL> | bash -s -- <language>
#
# Environment variables:
#   DEST_DIR  Destination directory (default: current directory)

set -euo pipefail

REPO_URL="https://github.com/taka1156/codespace-template.git"
BRANCH="dist"
BASE_DIR="sample_devcontainer"
DEST_DIR="${DEST_DIR:-.}"

if ! command -v git >/dev/null 2>&1; then
  echo "Error: git is not installed." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  printf '\033[?25h' > /dev/tty 2>/dev/null || true  # show the cursor again
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# Arrow-key selection menu (Bubble Tea style)
#   Usage: select_menu "<prompt>" option1 option2 ...
#   The result is stored in the MENU_RESULT variable.
select_menu() {
  local prompt="$1"
  shift
  local options=("$@")
  local count=${#options[@]}
  local selected=0 i key rest

  printf '%s\n' "$prompt" > /dev/tty
  printf '\033[?25l' > /dev/tty  # hide the cursor

  while true; do
    for ((i = 0; i < count; i++)); do
      if [ "$i" -eq "$selected" ]; then
        printf '\033[36m❯ %s\033[0m\033[K\n' "${options[$i]}" > /dev/tty
      else
        printf '  %s\033[K\n' "${options[$i]}" > /dev/tty
      fi
    done

    IFS= read -rsn1 key < /dev/tty || true
    if [ "$key" = $'\033' ]; then
      # arrow keys are 3 bytes: ESC [ A/B
      read -rsn2 -t 0.1 rest < /dev/tty || true
      key+="$rest"
    fi

    case "$key" in
      $'\033[A' | k) selected=$(((selected - 1 + count) % count)) ;;
      $'\033[B' | j) selected=$(((selected + 1) % count)) ;;
      '') break ;;  # Enter
      q | $'\033')
        printf '\033[?25h' > /dev/tty
        echo "Cancelled." >&2
        exit 130
        ;;
    esac

    printf '\033[%dA' "$count" > /dev/tty  # move back to the top of the menu and redraw
  done

  printf '\033[?25h' > /dev/tty
  MENU_RESULT="${options[$selected]}"
}

# Always read interactive input from the terminal (/dev/tty).
# This keeps it working when stdin is a pipe, e.g. `curl ... | bash`.
if (exec < /dev/tty) 2>/dev/null; then
  TTY_AVAILABLE=1
else
  TTY_AVAILABLE=0
fi

if [ "$TTY_AVAILABLE" -eq 0 ]; then
  echo "Error: no terminal (/dev/tty) available for interactive input." >&2
  exit 1
fi

# --- 1. Fetch only the needed directory (sparse checkout) ---
echo "Fetching templates..."
git clone --quiet --depth 1 --branch "$BRANCH" --filter=blob:none --sparse \
  "$REPO_URL" "$TMP_DIR/repo"
git -C "$TMP_DIR/repo" sparse-checkout set "$BASE_DIR"

SAMPLE_ROOT="$TMP_DIR/repo/$BASE_DIR"
if [ ! -d "$SAMPLE_ROOT" ]; then
  echo "Error: $BASE_DIR not found." >&2
  exit 1
fi

# --- 2. List available languages (subdirectories) ---
LANGS=()
while IFS= read -r d; do
  LANGS+=("$d")
done < <(find "$SAMPLE_ROOT" -mindepth 1 -maxdepth 1 -type d ! -name '.*' -exec basename {} \; | sort)

if [ "${#LANGS[@]}" -eq 0 ]; then
  echo "Error: no language directories found." >&2
  exit 1
fi

# --- 3. Decide the language (argument or interactive selection) ---
LANG_NAME="${1:-}"

if [ -z "$LANG_NAME" ]; then
  select_menu "Select a language (↑↓ / j k to move, Enter to confirm, q to cancel):" "${LANGS[@]}"
  LANG_NAME="$MENU_RESULT"
fi

if [ ! -d "$SAMPLE_ROOT/$LANG_NAME" ]; then
  echo "Error: '$LANG_NAME' does not exist." >&2
  echo "Available: ${LANGS[*]}" >&2
  exit 1
fi

# --- 4. Ask for the project name interactively ---
PROJECT_NAME=""
while [ -z "$PROJECT_NAME" ]; do
  read -r -p "Enter the project name: " PROJECT_NAME < /dev/tty
  # trim leading and trailing whitespace
  PROJECT_NAME="${PROJECT_NAME#"${PROJECT_NAME%%[![:space:]]*}"}"
  PROJECT_NAME="${PROJECT_NAME%"${PROJECT_NAME##*[![:space:]]}"}"
done

# --- 5. Copy to a staging directory and rewrite devcontainer.json ---
STAGE_DIR="$TMP_DIR/stage"
mkdir -p "$STAGE_DIR"
cp -a "$SAMPLE_ROOT/$LANG_NAME/." "$STAGE_DIR/"

# Escape for a JSON string (\ and ")
ESCAPED_NAME="${PROJECT_NAME//\\/\\\\}"
ESCAPED_NAME="${ESCAPED_NAME//\"/\\\"}"

JSON_FILES=()
while IFS= read -r f; do
  JSON_FILES+=("$f")
done < <(find "$STAGE_DIR" -name 'devcontainer.json' -type f)

if [ "${#JSON_FILES[@]}" -eq 0 ]; then
  echo "Warning: devcontainer.json not found; skipping the name rewrite." >&2
else
  for f in "${JSON_FILES[@]}"; do
    # Use awk instead of jq so JSON with comments (JSONC) is handled; replaces only the first "name"
    PROJECT_NAME_ESCAPED="$ESCAPED_NAME" awk '
      BEGIN { done = 0; n = ENVIRON["PROJECT_NAME_ESCAPED"] }
      !done && match($0, /"name"[ \t]*:[ \t]*"[^"]*"/) {
        pre  = substr($0, 1, RSTART - 1)
        post = substr($0, RSTART + RLENGTH)
        m    = substr($0, RSTART, RLENGTH)
        sub(/"[^"]*"$/, "", m)
        $0 = pre m "\"" n "\"" post
        done = 1
      }
      { print }
    ' "$f" > "$f.tmp" && mv "$f.tmp" "$f"
  done
fi

# --- 6. Place the files in the destination ---
mkdir -p "$DEST_DIR"

if [ -e "$DEST_DIR/.devcontainer" ] && [ -e "$STAGE_DIR/.devcontainer" ]; then
  read -r -p "$DEST_DIR/.devcontainer already exists. Overwrite? [y/N]: " ANSWER < /dev/tty
  case "$ANSWER" in
    [yY]*) ;;
    *) echo "Aborted."; exit 1 ;;
  esac
fi

cp -a "$STAGE_DIR/." "$DEST_DIR/"

echo
echo "Done: language='$LANG_NAME' / project name='$PROJECT_NAME'"
echo "Destination: $(cd "$DEST_DIR" && pwd)"