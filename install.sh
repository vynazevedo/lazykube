#!/usr/bin/env bash

main() {
  set -euo pipefail

  fail() { printf 'lazykube: %s\n' "$*" >&2; exit 1; }
  command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || fail 'Instale curl ou wget.'
  fetch() {
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL --retry 3 "$1" -o "$2"
    else
      wget -q -O "$2" "$1"
    fi
  }

  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    MINGW*|MSYS*|CYGWIN*) os=windows ;;
    *) fail 'Sistema nao suportado. Use Linux, macOS ou Windows com Git Bash/MSYS/Cygwin.' ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64|AMD64) arch=amd64 ;;
    aarch64|arm64|ARM64) arch=arm64 ;;
    *) fail 'Arquitetura nao suportada. Use amd64 ou arm64.' ;;
  esac

  base_url=${BASE_URL:-https://github.com/vynazevedo/lazykube/releases}
  base_url=${base_url%/}
  version=${VERSION:-}
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  if [[ -z "$version" ]]; then
    if command -v curl >/dev/null 2>&1; then
      latest=$(curl -fsSL --retry 3 -o /dev/null -w '%{url_effective}' "$base_url/latest")
    else
      fetch "$base_url/latest" "$tmp/latest.html"
      latest=$(sed -n 's|.*releases/expanded_assets/\([^"?<> ]*\).*|\1|p' "$tmp/latest.html" | head -n 1)
    fi
    version=${latest##*/}
  fi
  [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] || fail "Versao invalida: $version. Informe VERSION=vX.Y.Z."

  extension=tar.gz
  binary=lazykube
  if [[ "$os" == windows ]]; then
    extension=zip
    binary=lazykube.exe
    command -v unzip >/dev/null 2>&1 || fail 'Instale unzip para extrair a versao Windows.'
  fi
  archive="lazykube_${version#v}_${os}_${arch}.${extension}"
  printf 'Baixando lazykube %s (%s/%s)...\n' "$version" "$os" "$arch"
  fetch "$base_url/download/$version/$archive" "$tmp/$archive"
  fetch "$base_url/download/$version/checksums.txt" "$tmp/checksums.txt"
  expected=$(awk -v name="$archive" '$2 == name {print $1}' "$tmp/checksums.txt")
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || fail "Checksum ausente ou invalido para $archive."
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$tmp/$archive")
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$tmp/$archive")
  else
    fail 'Instale sha256sum ou shasum para verificar o download.'
  fi
  [[ "${actual%% *}" == "$expected" ]] || fail 'SHA-256 divergente. Instalacao cancelada.'
  printf 'SHA-256 verificado.\n'

  if [[ "$os" == windows ]]; then
    unzip -q "$tmp/$archive" "$binary" -d "$tmp"
  else
    tar -xzf "$tmp/$archive" -C "$tmp" "$binary"
  fi
  [[ -f "$tmp/$binary" && ! -L "$tmp/$binary" ]] || fail 'Binario ausente ou invalido no arquivo.'
  if [[ -n "${INSTALL_DIR:-}" ]]; then
    destination=$INSTALL_DIR
  elif [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
    destination=/usr/local/bin
  else
    destination="$HOME/.local/bin"
  fi
  mkdir -p "$destination" || fail "Nao foi possivel criar $destination. Escolha INSTALL_DIR ou ajuste as permissoes com sudo."
  [[ -w "$destination" ]] || fail "Sem permissao em $destination. Escolha INSTALL_DIR ou ajuste as permissoes com sudo."
  staged=$(mktemp "$destination/.lazykube.XXXXXX")
  trap 'rm -rf "$tmp"; rm -f "$staged"' EXIT
  cp "$tmp/$binary" "$staged"
  chmod 0755 "$staged"
  [[ ! -d "$destination/$binary" ]] || fail "Destino e um diretorio: $destination/$binary. Instalacao cancelada."
  mv -f "$staged" "$destination/$binary"
  printf 'Instalado em %s/%s\n' "$destination" "$binary"
  case ":$PATH:" in
    *":$destination:"*) ;;
    *) printf 'Adicione %s ao PATH para executar lazykube.\n' "$destination" ;;
  esac
}

main "$@"
