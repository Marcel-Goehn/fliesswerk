#!/bin/sh
# shellcheck disable=SC2016 # the profile lines keep $HOME and $PATH literal
# Installs fliesswerk for the current user on Linux and macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/Marcel-Goehn/fliesswerk/main/install.sh | sh
#
# FLIESSWERK_VERSION      a release to install instead of the latest, like v1.2.3
# FLIESSWERK_DOWNLOAD_URL a mirror of the GitHub releases URL
# FLIESSWERK_NO_MODIFY_PATH set to leave your shell profile alone
#
# The binary is copied byte for byte: changing a macOS binary after the build
# breaks the ad-hoc signature Go gave it. See docs/install.md.
set -eu

# Everything is in main, so a download cut off halfway runs nothing.
main() {
	archive=$(archive_name)
	base=${FLIESSWERK_DOWNLOAD_URL:-https://github.com/Marcel-Goehn/fliesswerk/releases}
	if [ -n "${FLIESSWERK_VERSION:-}" ]; then
		url="$base/download/v${FLIESSWERK_VERSION#v}"
	else
		url="$base/latest/download"
	fi
	dir="$HOME/.local/bin"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	say "Downloading $archive"
	curl -fsSL --retry 3 -o "$tmp/$archive" "$url/$archive"
	curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$url/checksums.txt"
	verify "$tmp/$archive" "$archive" "$tmp/checksums.txt"
	tar -xzf "$tmp/$archive" -C "$tmp" fliesswerk

	mkdir -p "$dir"
	# Replaced by a rename, never overwritten in place: macOS kills a signed
	# binary whose file changed under it, and Linux refuses to write to a
	# running one.
	cp "$tmp/fliesswerk" "$dir/.fliesswerk.new"
	chmod 755 "$dir/.fliesswerk.new"
	mv -f "$dir/.fliesswerk.new" "$dir/fliesswerk"
	say "Installed fliesswerk to $dir/fliesswerk"
	add_fwk
	add_to_path
}

# add_fwk adds the short name, unless the name is already someone else's.
add_fwk() {
	if [ -L "$dir/fwk" ] && [ "$(readlink "$dir/fwk")" = fliesswerk ]; then
		return
	fi
	if taken=$(command -v fwk); then
		say "Not adding fwk: $taken already has that name. Use fliesswerk instead."
	elif [ -e "$dir/fwk" ] || [ -L "$dir/fwk" ]; then
		say "Not adding fwk: $dir/fwk already exists. Use fliesswerk instead."
	else
		ln -s fliesswerk "$dir/fwk"
		say "Added fwk as a short name for fliesswerk"
	fi
}

# add_to_path makes new shells find $dir, through the profile of the user's
# shell. Running the script again adds nothing.
add_to_path() {
	case ":$PATH:" in
	*":$dir:"*) return ;;
	esac
	if [ -n "${FLIESSWERK_NO_MODIFY_PATH:-}" ]; then
		say "$dir isn't on your PATH; add it to run fliesswerk by name"
		return
	fi
	line='export PATH="$HOME/.local/bin:$PATH"'
	case "$(basename "${SHELL:-sh}")" in
	zsh) profile="${ZDOTDIR:-$HOME}/.zshrc" ;;
	bash)
		# On a Mac, bash reads only the first of these it finds, so a new
		# .bash_profile would switch an existing .profile off.
		if [ "$(uname -s)" = Darwin ] && { [ -e "$HOME/.bash_profile" ] || [ ! -e "$HOME/.profile" ]; }; then
			profile="$HOME/.bash_profile"
		elif [ "$(uname -s)" = Darwin ]; then
			profile="$HOME/.profile"
		else
			profile="$HOME/.bashrc"
		fi
		;;
	fish)
		profile="$HOME/.config/fish/conf.d/fliesswerk.fish"
		line='fish_add_path "$HOME/.local/bin"'
		mkdir -p "$HOME/.config/fish/conf.d"
		;;
	*) profile="$HOME/.profile" ;;
	esac
	if ! grep -qsF "$line" "$profile"; then
		echo "$line" >>"$profile"
		say "Added $dir to your PATH in $profile"
	fi
	say "Open a new shell to run fliesswerk by name"
}

archive_name() {
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "this script is for Linux and macOS; on Windows use install.ps1" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "no release for this processor ($(uname -m)); see https://github.com/Marcel-Goehn/fliesswerk/blob/main/docs/install.md to build from source" ;;
	esac
	echo "fliesswerk_${os}_${arch}.tar.gz"
}

# verify stops unless the file's SHA-256 is the one checksums.txt lists for name.
verify() {
	want=$(awk -v name="$2" '$2 == name { print $1 }' "$3")
	[ -n "$want" ] || die "checksums.txt doesn't list $2, so it can't be verified; nothing was installed"
	if command -v sha256sum >/dev/null 2>&1; then
		got=$(sha256sum "$1" | awk '{ print $1 }')
	elif command -v shasum >/dev/null 2>&1; then
		got=$(shasum -a 256 "$1" | awk '{ print $1 }')
	else
		die "neither sha256sum nor shasum is installed, so the download can't be verified"
	fi
	[ "$got" = "$want" ] || die "the checksum of $2 doesn't match checksums.txt, so the download is damaged or was tampered with; nothing was installed"
}

say() { echo "fliesswerk install: $*"; }
die() {
	echo "fliesswerk install: error: $*" >&2
	exit 1
}

main
