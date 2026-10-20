#!/bin/sh
# Installs ouija on Linux or macOS from a GitHub release: downloads the
# archive for this machine, checks it against the release's SHA256SUMS and
# puts ouija and ouija-mcp in ~/.local/bin.
#
#   curl -fsSL https://raw.githubusercontent.com/discorgento/ouija/main/scripts/install.sh | sh
#
# Options, after `sh -s --` when piped:
#   --version 0.2.0   a given release instead of the latest
#   --dir DIR         where the binaries go (default ~/.local/bin)
#
# Nothing needs root: the folder is your user's. ouija is a terminal
# program - after this, run `ouija` in a project.
set -eu

repo="discorgento/ouija"
version="latest"
dir="${HOME}/.local/bin"

while [ $# -gt 0 ]; do
    case "$1" in
        --version) version="${2:?--version needs a value}"; shift 2 ;;
        --version=*) version="${1#*=}"; shift ;;
        --dir) dir="${2:?--dir needs a value}"; shift 2 ;;
        --dir=*) dir="${1#*=}"; shift ;;
        -h|--help) sed -n '2,14p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "install.sh: unknown option $1 (try --help)" >&2; exit 2 ;;
    esac
done

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

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

api="https://api.github.com/repos/${repo}/releases"
if [ "$version" = "latest" ]; then
    # For a few minutes after a release is cut, CI is still building its
    # archives: take the newest release that has them.
    all=$(get "${api}?per_page=10" | urls) || die "could not reach the GitHub API."
    found=""
    for tag in $(printf '%s\n' "$all" | sed -n 's#.*/releases/download/\([^/]*\)/.*#\1#p' | awk '!seen[$0]++'); do
        found=$(printf '%s\n' "$all" | grep "/download/${tag}/" | pick)
        [ -n "$found" ] && break
        say "${tag} has no ${os}/${arch} archive yet (still being built?); trying the release before it"
    done
    [ -n "$found" ] || die "no release with a ${os}/${arch} archive and SHA256SUMS found."
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
say "installed in ${dir}"

"$dir/ouija" version

case ":${PATH}:" in
    *":${dir}:"*) ;;
    *)
        say ""
        say "${dir} is not on your PATH. Add it to your shell's startup file:"
        case "${SHELL:-}" in
            */zsh) say "  echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.zshrc" ;;
            */fish) say "  fish_add_path ${dir}" ;;
            *) say "  echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.bashrc" ;;
        esac
        say "then open a new terminal."
        ;;
esac

say ""
say "Next:"
say "  ouija setup      # API keys and the default model (or ollama, local and free)"
say "  ouija            # in a project folder: the chat"
