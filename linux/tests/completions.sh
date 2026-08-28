#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'completion test failed: %s\n' "$1" >&2
    exit 1
}

cli_file="${ROOT_DIR}/kikimora"
help_file="${ROOT_DIR}/files/kikimora-cli/help.sh"
bash_file="${ROOT_DIR}/completions/kikimora.bash"
zsh_file="${ROOT_DIR}/completions/_kikimora"
fish_file="${ROOT_DIR}/completions/kikimora.fish"

for file in "$cli_file" "$help_file" "$bash_file" "$zsh_file" "$fish_file"; do
    [[ -f "$file" ]] || fail "missing CLI/help/completion file: $file"
done

grep -Fq 'complete -F _kikimora_complete kikimora kk' "$bash_file" || \
    fail 'bash completion does not register kikimora and kk'

grep -Fq '#compdef kikimora kk' "$zsh_file" || \
    fail 'zsh completion does not register kikimora and kk'
grep -Fq '_arguments' "$zsh_file" || \
    fail 'zsh completion does not define argument completion'

grep -Fq 'complete -c kikimora -c kk' "$fish_file" || \
    fail 'fish completion does not register kikimora and kk'

readonly -a top_level_commands=(
    install upgrade uninstall verify doctor debuglog diag backup restore
    start stop restart enable disable status interfaces dns config profiles
    domains routes logs completion version help
)

readonly bash_top_level="install upgrade uninstall verify doctor debuglog diag backup restore start stop restart enable disable status interfaces dns config profiles domains routes logs completion version help --version -V --help -h"
grep -Fq "compgen -W '${bash_top_level}'" "$bash_file" || \
    fail 'bash top-level command list is incomplete or out of sync'

for command in "${top_level_commands[@]}"; do
    grep -Eq "^[[:space:]]*([^)]*\\|)?${command}(\\|[^)]*)?\\)" "$cli_file" || \
        fail "CLI dispatcher does not expose expected command: $command"
    grep -Fq "'${command}:" "$zsh_file" || \
        fail "zsh completion does not expose top-level command: $command"
    grep -Fq -- "-n '__kikimora_no_command' -a ${command} " "$fish_file" || \
        fail "fish completion does not expose top-level command: $command"
    grep -Eq "^  ${command}([[:space:],]|$)" "$help_file" || \
        fail "main help does not document top-level command: $command"
done

[[ "$(grep -Fc -- '--leshy-binary' "$bash_file")" -ge 2 ]] || \
    fail 'bash install/upgrade completion does not cover --leshy-binary'
[[ "$(grep -Fc -- '--leshy-binary' "$zsh_file")" -ge 2 ]] || \
    fail 'zsh install/upgrade completion does not cover --leshy-binary'
[[ "$(grep -Fc -- '-l leshy-binary' "$fish_file")" -ge 2 ]] || \
    fail 'fish install/upgrade completion does not cover --leshy-binary'

grep -Fq 'upgrade PATH [INSTALL OPTIONS]' "$help_file" || \
    fail 'main help does not document forwarded upgrade installer options'
grep -Fq 'profiles status' "$help_file" || \
    fail 'main help does not document profiles status alias'
grep -Fq 'domains status' "$help_file" || \
    fail 'main help does not document domains status'
grep -Fq 'routes status' "$help_file" || \
    fail 'main help does not document routes status'
grep -Fq 'completion bash' "$help_file" || \
    fail 'main help does not document the completion command'

grep -Fq "compgen -W 'status list add remove edit import export default help -h --help'" "$bash_file" || \
    fail 'bash domains completion mixes zones/modes into the command list'
grep -Fq "_values 'domains command' status list add remove edit import export default help -h --help" "$zsh_file" || \
    fail 'zsh domains completion mixes zones/modes into the command list'
grep -Fq "__kikimora_needs_nested_command domains' -a 'status list add remove edit import export default help'" "$fish_file" || \
    fail 'fish domains completion mixes zones/modes into the command list'

grep -Fq "compgen -W 'status list add remove edit import export help -h --help'" "$bash_file" || \
    fail 'bash routes completion mixes zones into the command list'
grep -Fq "_values 'routes command' status list add remove edit import export help -h --help" "$zsh_file" || \
    fail 'zsh routes completion mixes zones into the command list'
grep -Fq "__kikimora_needs_nested_command routes' -a 'status list add remove edit import export help'" "$fish_file" || \
    fail 'fish routes completion mixes zones into the command list'

printf 'CLI help and shell completion command coverage: OK\n'
