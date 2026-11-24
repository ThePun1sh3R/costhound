#!/bin/sh
# Install costhound from GitHub Releases (Linux, macOS, AWS CloudShell).
# Usage: curl -fsSL https://raw.githubusercontent.com/ThePun1sh3R/costhound/main/install.sh | sh
# Pin:  COSTHOUND_VERSION=v0.3.0 … | sh
# Dir:  COSTHOUND_INSTALL_DIR=~/.local/bin (default)
set -eu

RELEASES_PAGE='https://github.com/ThePun1sh3R/costhound/releases'
LATEST_URL='https://github.com/ThePun1sh3R/costhound/releases/latest'
DOWNLOAD_BASE='https://github.com/ThePun1sh3R/costhound/releases/download'

download_to() {
	# $1 = url, $2 = dest path
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		echo "need curl or wget" >&2
		exit 1
	fi
}

# Resolve latest tag via /releases/latest redirect (no GitHub API).
# Prints tag to stdout; returns non-zero on failure (never a bare curl/wget abort).
resolve_latest_tag() {
	_url=
	if command -v curl >/dev/null 2>&1; then
		_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$LATEST_URL") || return 1
	elif command -v wget >/dev/null 2>&1; then
		# 302 to .../releases/tag/vX.Y.Z; do not follow so Location is visible.
		_url=$(wget --max-redirect=0 --server-response --spider "$LATEST_URL" 2>&1 \
			| sed -n 's/^  [Ll]ocation: *//p' | tail -n 1 | tr -d '\r') || true
		[ -n "$_url" ] || return 1
	else
		echo "need curl or wget" >&2
		return 1
	fi
	_tag=$(printf '%s\n' "$_url" | sed -n 's|.*/releases/tag/\([^/?#]*\).*|\1|p')
	[ -n "$_tag" ] || return 1
	printf '%s\n' "$_tag"
}

download_failed() {
	# $1 = url that failed
	echo "download failed: $1" >&2
	echo "see ${RELEASES_PAGE}" >&2
	case "${tag}" in
		v*) ;;
		*)
			echo "hint: version tags usually start with v (try COSTHOUND_VERSION=v${tag})" >&2
			;;
	esac
	exit 1
}

main() {
	uname_s=$(uname -s | tr '[:upper:]' '[:lower:]')
	case "$uname_s" in
		linux*) os=linux ;;
		darwin*) os=darwin ;;
		mingw*|msys*|cygwin*)
			echo "Windows isn't supported by this installer." >&2
			echo "Download a release from ${RELEASES_PAGE}" >&2
			exit 1
			;;
		*)
			echo "Unsupported OS (${uname_s}). See ${RELEASES_PAGE}" >&2
			exit 1
			;;
	esac

	uname_m=$(uname -m)
	case "$uname_m" in
		x86_64|amd64) arch=amd64 ;;
		aarch64|arm64) arch=arm64 ;;
		*)
			echo "Unsupported architecture (${uname_m}). See ${RELEASES_PAGE}" >&2
			exit 1
			;;
	esac

	if [ -n "${COSTHOUND_VERSION:-}" ]; then
		tag=$COSTHOUND_VERSION
	else
		if ! tag=$(resolve_latest_tag); then
			echo "could not find the latest version: set COSTHOUND_VERSION=vX.Y.Z" >&2
			echo "see ${RELEASES_PAGE}" >&2
			exit 1
		fi
	fi

	asset="costhound_${tag}_${os}_${arch}"
	dest=${COSTHOUND_INSTALL_DIR:-$HOME/.local/bin}
	asset_url="${DOWNLOAD_BASE}/${tag}/${asset}"
	sums_url="${DOWNLOAD_BASE}/${tag}/SHA256SUMS"

	tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/costhound.XXXXXX")
	trap 'rm -rf "$tmpdir"' EXIT INT TERM HUP

	download_to "$asset_url" "$tmpdir/$asset" || download_failed "$asset_url"
	download_to "$sums_url" "$tmpdir/SHA256SUMS" || download_failed "$sums_url"

	line=$(grep " ${asset}$" "$tmpdir/SHA256SUMS" || true)
	if [ -z "$line" ]; then
		echo "SHA256SUMS has no line for ${asset}; refusing to install" >&2
		exit 1
	fi

	# Verify only this asset's line (other platform rows are ignored).
	(
		cd "$tmpdir"
		if command -v sha256sum >/dev/null 2>&1; then
			printf '%s\n' "$line" | sha256sum -c -
		elif command -v shasum >/dev/null 2>&1; then
			printf '%s\n' "$line" | shasum -a 256 -c -
		else
			echo "need sha256sum or shasum to verify the download" >&2
			exit 1
		fi
	)

	mkdir -p "$dest"
	# Prefer install(1); fall back to cp + chmod for minimal environments.
	if command -v install >/dev/null 2>&1; then
		install -m 755 "$tmpdir/$asset" "$dest/costhound"
	else
		cp "$tmpdir/$asset" "$dest/costhound"
		chmod 755 "$dest/costhound"
	fi

	echo "installed ${dest}/costhound"
	case ":${PATH}:" in
		*":${dest}:"*) ;;
		*)
			echo "${dest} is not on your PATH; add it, for example:"
			echo "  export PATH=\"${dest}:\$PATH\""
			;;
	esac
	"$dest/costhound" -version
}

main "$@"
