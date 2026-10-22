#!/bin/sh
# Installs ouija on Linux or macOS from a GitHub release: downloads the
# archive for this machine, checks it against the release's SHA256SUMS and
# puts ouija and ouija-mcp in ~/.local/bin.
#
#   curl -fsSL https://raw.githubusercontent.com/discorgento/ouija/main/scripts/install.sh | sh
#
# Options, after `sh -s --` when piped:
#   --version 0.2.0   a given release instead of the latest
#   --prerelease      let the latest be a pre-release too (default: stable only)
#   --dir DIR         where the binaries go (default ~/.local/bin)
#   --no-prompt       ask nothing: no PATH change, no setup
#   --require-provenance  refuse to install unless the archive's build
#                     provenance is verified (needs gh, logged in)
#
# Nothing needs root: the folder is your user's. In a terminal it offers to
# add the folder to your PATH and to run `ouija setup`; anywhere else (CI, a
# pipe without a terminal) it only prints what to do.
set -eu

repo="discorgento/ouija"
version="latest"
dir="${HOME}/.local/bin"
prompt=yes
require_provenance=no
prerelease=no

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="${2:?--version needs a value}"; shift 2 ;;
        --version=*) version="${1#*=}"; shift ;;
        --dir) dir="${2:?--dir needs a value}"; shift 2 ;;
        --dir=*) dir="${1#*=}"; shift ;;
        --no-prompt) prompt=no; shift ;;
        --prerelease) prerelease=yes; shift ;;
        --require-provenance) require_provenance=yes; shift ;;
        -h|--help) sed -n '2,18p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "install.sh: unknown option $1 (try --help)" >&2; exit 2 ;;
    esac
done

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# The folder goes into a shell rc file inside double quotes, where $, `, \
# and " would run or break as code in every new shell.
case "$dir" in
    *[\"\$\`\\]*) die "--dir must not contain \", \$, \` or \\: ${dir}" ;;
    /*) ;;
    # A relative folder on PATH is looked up from whatever directory a shell
    # is in: a repository could put its own "ouija" first.
    *) die "--dir must be an absolute path (${dir} is not; for your home use \$HOME/...)" ;;
esac

case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "this script covers Linux and macOS; on Windows use scripts/install.ps1." ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "ouija ships amd64 and arm64 builds; this machine is $(uname -m)." ;;
esac

if command -v curl >/dev/null 2>&1; then
    get() { curl -fsSL ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} "$1"; }
    fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
    get() { wget -qO- ${GITHUB_TOKEN:+--header="Authorization: Bearer $GITHUB_TOKEN"} "$1"; }
    fetch() { wget -qO "$2" "$1"; }
else
    die "needs curl or wget."
fi
if command -v sha256sum >/dev/null 2>&1; then
    sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
    sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
    die "needs sha256sum or shasum to check the download."
fi

# urls prints the download URLs in a release listing, one per line.
urls() { grep -o '"browser_download_url": *"[^"]*"' | sed 's/.*"\(https[^"]*\)"$/\1/'; }

# pick prints the archive URL of one release's URLs (on stdin) and its
# SHA256SUMS URL, or nothing. v0.1.0 named its archives ouija-<os>-<arch>.tar.gz.
pick() {
    list=$(cat)
    a=$(printf '%s\n' "$list" | grep "/ouija_[^/]*_${os}_${arch}\.tar\.gz$" | head -n 1 || true)
    [ -n "$a" ] || a=$(printf '%s\n' "$list" | grep "/ouija-${os}-${arch}\.tar\.gz$" | head -n 1 || true)
    s=$(printf '%s\n' "$list" | grep '/SHA256SUMS$' | head -n 1 || true)
    if [ -n "$a" ] && [ -n "$s" ]; then printf '%s %s\n' "$a" "$s"; fi
}

# stable prints the tags of a release listing (on stdin) that are not
# pre-releases, newest first; with --prerelease, every tag. The API puts a
# release's "tag_name" before its "prerelease"; quotes inside the notes are
# escaped, so the notes cannot fake either field.
stable() {
    grep -oE '"(tag_name|prerelease)": *("[^"]*"|true|false)' |
        awk -v all="$prerelease" '
            /"tag_name"/ { t = $0; sub(/^"tag_name": *"/, "", t); sub(/"$/, "", t); next }
            t != "" && (all == "yes" || /false$/) { print t }
            { t = "" }'
}

api="https://api.github.com/repos/${repo}/releases"
if [ "$version" = "latest" ]; then
    # For a few minutes after a release is cut, CI is still building its
    # archives: take the newest release that has them. Pre-releases are
    # skipped unless --prerelease asks for them.
    listing=$(get "${api}?per_page=10") || die "could not reach the GitHub API."
    all=$(printf '%s\n' "$listing" | urls || true)
    found=""
    for tag in $(printf '%s\n' "$listing" | stable); do
        found=$(printf '%s\n' "$all" | grep -F "/download/${tag}/" | pick)
        [ -n "$found" ] && break
        say "${tag} has no ${os}/${arch} archive yet (still being built?); trying the release before it"
    done
    [ -n "$found" ] || die "no release with a ${os}/${arch} archive and SHA256SUMS found (pre-releases are skipped: --prerelease includes them)."
else
    tag="v${version#v}"
    rel=$(get "${api}/tags/${tag}" 2>/dev/null) || die "there is no release ${tag} (see https://github.com/${repo}/releases)."
    found=$(printf '%s\n' "$rel" | urls | pick)
    [ -n "$found" ] || die "release ${tag} has no ${os}/${arch} archive or no SHA256SUMS (if it was cut minutes ago, CI may still be building it)."
fi
archive_url=${found% *}
sums_url=${found#* }
name=${archive_url##*/}
say "ouija $(printf '%s' "$archive_url" | sed 's#.*/download/\([^/]*\)/.*#\1#') - ${name}"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t ouija-install)
trap 'rm -rf "$tmp"' EXIT INT TERM
fetch "$archive_url" "$tmp/$name" || die "could not download ${name}."
fetch "$sums_url" "$tmp/SHA256SUMS" || die "could not download SHA256SUMS."

want=$(awk -v n="$name" '{ f = $2; sub(/^\*/, "", f); if (f == n) { print tolower($1); exit } }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "${name} is not listed in SHA256SUMS."
got=$(sha "$tmp/$name" | tr 'A-F' 'a-f')
[ "$got" = "$want" ] || die "checksum mismatch for ${name}: got ${got}, want ${want} - not installed."
say "checksum ok"

# The checksum only proves the archive matches SHA256SUMS, and both come from
# the same release. The build provenance ties the archive to this repository's
# release workflow (Sigstore-signed by GitHub Actions): a swapped archive has
# none, and one built by any other workflow of the repository is refused too,
# as is one the release workflow built from another branch (a modified copy
# of it pushed there): releases run on pushes to main only.
# Checking it needs the GitHub CLI, logged in or with a token.
workflow="${repo}/.github/workflows/release.yml"
source_ref="refs/heads/main"
provenance=no
if command -v gh >/dev/null 2>&1 && { [ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ] || gh auth status >/dev/null 2>&1; }; then
    if out=$(gh attestation verify "$tmp/$name" --repo "$repo" --signer-workflow "$workflow" --source-ref "$source_ref" 2>&1); then
        provenance=yes
        say "provenance ok (built by ${workflow})"
    else
        printf '%s\n' "$out" | tail -n 3 >&2
        die "provenance check failed for ${name}: it was not built by ${workflow} on main - not installed."
    fi
elif [ "$require_provenance" = yes ]; then
    die "--require-provenance: the GitHub CLI (gh), logged in, is needed to verify the build provenance - not installed."
else
    say "WARNING: provenance NOT verified - only the checksum was checked, and it comes from the same release as the archive."
    say "         Install the GitHub CLI and log in (gh auth login) to have it verified, or check by hand:"
    say "         gh attestation verify ${name} --repo ${repo} --signer-workflow ${workflow} --source-ref ${source_ref}"
fi

mkdir -p "$tmp/x"
tar -xzf "$tmp/$name" -C "$tmp/x" || die "could not extract ${name}."
bin=$(find "$tmp/x" -type f -name ouija | head -n 1)
[ -n "$bin" ] || die "no ouija binary in ${name}."
mkdir -p "$dir"
for b in ouija ouija-mcp; do
    if [ -f "$(dirname "$bin")/$b" ]; then
        # Through a temp name, so a running ouija is replaced, not overwritten.
        cp "$(dirname "$bin")/$b" "$dir/.$b.new"
        chmod 755 "$dir/.$b.new"
        mv -f "$dir/.$b.new" "$dir/$b"
    fi
done
if [ "$provenance" = yes ]; then
    say "installed in ${dir}"
else
    say "installed in ${dir} (checksum only: provenance NOT verified)"
fi

"$dir/ouija" version

# Questions go to the terminal, not stdin: under `curl … | sh`, stdin is the
# script itself. No terminal (CI, cron), or --no-prompt: nothing is asked.
interactive=no
if [ "$prompt" = yes ] && [ -z "${CI:-}" ] && (exec </dev/tty) 2>/dev/null; then
    interactive=yes
fi
ask() {
    printf '%s [Y/n] ' "$1" >/dev/tty
    read -r answer </dev/tty || answer=n
    case "$answer" in ""|y|Y|yes|s|S|sim) return 0 ;; *) return 1 ;; esac
}

on_path=yes
case ":${PATH}:" in *":${dir}:"*) ;; *) on_path=no ;; esac
if [ "$on_path" = no ]; then
    case "${SHELL:-}" in
        */zsh) rc="$HOME/.zshrc"; line="export PATH=\"${dir}:\$PATH\"" ;;
        */fish) rc="$HOME/.config/fish/conf.d/ouija.fish"; line="fish_add_path \"${dir}\"" ;;
        *) rc="$HOME/.bashrc"; [ "$(uname -s)" = Darwin ] && rc="$HOME/.bash_profile"; line="export PATH=\"${dir}:\$PATH\"" ;;
    esac
    say ""
    if [ "$interactive" = yes ] && ask "${dir} is not on your PATH. Add it in ${rc}?"; then
        mkdir -p "$(dirname "$rc")"
        grep -qsF "$line" "$rc" || printf '\n# ouija\n%s\n' "$line" >> "$rc"
        say "added to ${rc}: new terminals will find ouija"
    else
        say "${dir} is not on your PATH. Add this line to ${rc}, then open a new terminal:"
        say "  ${line}"
    fi
    PATH="${dir}:${PATH}" # this script's own calls below
fi

say ""
if [ "$interactive" = yes ] && ask "Set up ouija now (provider, API key, model)?"; then
    "$dir/ouija" setup </dev/tty >/dev/tty 2>&1 || say "setup did not finish: run \`ouija setup\` to try again"
    say ""
    say "Next: cd into a project and run \`ouija\`."
else
    say "Next:"
    say "  ouija setup      # provider, API key and default model (or Ollama: local and free)"
    say "  ouija            # in a project folder: the chat"
fi
