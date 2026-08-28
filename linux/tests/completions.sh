#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'completion test failed: %s\n' "$1" >&2
    exit 1
}

bash_file="${ROOT_DIR}/completions/kikimora.bash"
zsh_file="${ROOT_DIR}/completions/_kikimora"
fish_file="${ROOT_DIR}/completions/kikimora.fish"

for file in "$bash_file" "$zsh_file" "$fish_file"; do
    [[ -f "$file" ]] || fail "missing completion file: $file"
done

grep -Fq 'complete -F _kikimora_complete kikimora kk' "$bash_file" || \
    fail 'bash completion does not register kikimora and kk'

grep -Fq '#compdef kikimora kk' "$zsh_file" || \
    fail 'zsh completion does not register kikimora and kk'
grep -Fq '_arguments' "$zsh_file" || \
    fail 'zsh completion does not define argument completion'

grep -Fq 'complete -c kikimora -c kk' "$fish_file" || \
    fail 'fish completion does not register kikimora and kk'

printf 'Shell completion files: OK\n'
