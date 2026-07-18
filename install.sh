#!/bin/sh

set -eu

repository="${MUSIC2BB_REPOSITORY:-bagags/music2bb-go}"
release_origin="${MUSIC2BB_RELEASE_ORIGIN:-https://github.com}"
install_dir="${MUSIC2BB_INSTALL_DIR:-${HOME}/.local/bin}"
data_dir="${MUSIC2BB_DATA_DIR:-${HOME}/.local/share/music2bb}"

fail() {
  printf 'music2bb installer: %s\n' "$*" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
  Darwin) platform="darwin" ;;
  Linux) platform="linux" ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) architecture="amd64" ;;
  arm64|aarch64) architecture="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

package="music2bb-${platform}-${architecture}"
archive="${package}.tar.gz"
if [ -n "${MUSIC2BB_VERSION:-}" ]; then
  release_base="${release_origin}/${repository}/releases/download/${MUSIC2BB_VERSION}"
else
  release_base="${release_origin}/${repository}/releases/latest/download"
fi

temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/music2bb-install.XXXXXX")"
temporary_binary="${install_dir}/.music2bb-install.$$"
cleanup() {
  rm -rf "$temporary_dir"
  rm -f "$temporary_binary"
}
trap cleanup EXIT HUP INT TERM

printf 'Downloading %s...\n' "$archive"
curl --fail --location --retry 3 --silent --show-error \
  --output "${temporary_dir}/${archive}" "${release_base}/${archive}"
curl --fail --location --retry 3 --silent --show-error \
  --output "${temporary_dir}/${archive}.sha256" "${release_base}/${archive}.sha256"

expected_checksum="$(awk 'NR == 1 { print $1 }' "${temporary_dir}/${archive}.sha256")"
[ "${#expected_checksum}" -eq 64 ] || fail "invalid release checksum"
if command -v sha256sum >/dev/null 2>&1; then
  actual_checksum="$(sha256sum "${temporary_dir}/${archive}" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual_checksum="$(shasum -a 256 "${temporary_dir}/${archive}" | awk '{ print $1 }')"
else
  fail "sha256sum or shasum is required"
fi
[ "$actual_checksum" = "$expected_checksum" ] || fail "release checksum mismatch"

tar -xzf "${temporary_dir}/${archive}" -C "$temporary_dir"
source_dir="${temporary_dir}/${package}"
[ -f "${source_dir}/music2bb" ] || fail "release archive does not contain music2bb"

mkdir -p "$install_dir" "$data_dir"
cp "${source_dir}/music2bb" "$temporary_binary"
chmod 0755 "$temporary_binary"
mv -f "$temporary_binary" "${install_dir}/music2bb"

for bundled_file in "$source_dir"/*; do
  [ -f "$bundled_file" ] || continue
  [ "$(basename "$bundled_file")" = "music2bb" ] && continue
  cp "$bundled_file" "$data_dir/"
done

path_was_updated=false
case ":${PATH}:" in
  *:"${install_dir}":*) ;;
  *)
    if [ "$install_dir" = "${HOME}/.local/bin" ] && [ "${MUSIC2BB_NO_PATH_UPDATE:-0}" != "1" ]; then
      path_marker="# Added by the music2bb installer"
      path_line='case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac'
      add_path_profile() {
        profile="$1"
        if [ ! -f "$profile" ] || ! grep -F "$path_marker" "$profile" >/dev/null 2>&1; then
          {
            printf '\n%s\n' "$path_marker"
            printf '%s\n' "$path_line"
          } >> "$profile"
          path_was_updated=true
        fi
      }
      shell_name="$(basename "${SHELL:-sh}")"
      case "$shell_name" in
        zsh)
          add_path_profile "${HOME}/.zprofile"
          add_path_profile "${HOME}/.zshrc"
          ;;
        bash)
          add_path_profile "${HOME}/.profile"
          add_path_profile "${HOME}/.bashrc"
          [ ! -f "${HOME}/.bash_profile" ] || add_path_profile "${HOME}/.bash_profile"
          ;;
        *) add_path_profile "${HOME}/.profile" ;;
      esac
    fi
    ;;
esac

printf 'Installed music2bb to %s\n' "${install_dir}/music2bb"
if [ "$path_was_updated" = true ]; then
  printf 'PATH was updated. Open a new terminal, or run: export PATH="$HOME/.local/bin:$PATH"\n'
elif ! command -v music2bb >/dev/null 2>&1; then
  printf 'Add %s to PATH to run music2bb from any directory.\n' "$install_dir"
fi
