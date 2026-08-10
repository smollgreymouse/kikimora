_kikimora_complete() {
    local cur prev command
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    command="${COMP_WORDS[1]:-}"

    if (( COMP_CWORD == 1 )); then
        COMPREPLY=( $(compgen -W 'install upgrade uninstall verify doctor debuglog diag backup restore start stop restart enable disable status interfaces dns config domains routes logs completion version help' -- "$cur") )
        return
    fi

    case "$command" in
        dns)
            COMPREPLY=( $(compgen -W 'enable disable suspend resume status help' -- "$cur") )
            ;;
        domains)
            COMPREPLY=( $(compgen -W 'status list add remove edit import export default direct primary secondary none primary secondary bypass --primary --secondary --bypass --help' -- "$cur") )
            ;;
        routes)
            COMPREPLY=( $(compgen -W 'status list add remove edit import export primary secondary --primary --secondary --help' -- "$cur") )
            ;;
        config)
            COMPREPLY=( $(compgen -W 'show edit validate help' -- "$cur") )
            ;;
        install)
            COMPREPLY=( $(compgen -W '--primary-interface --secondary-interface --non-interactive --stop-services --yes -y --help' -- "$cur") )
            ;;
        logs)
            COMPREPLY=( $(compgen -W '-f --follow --no-follow --all -n --lines --help' -- "$cur") )
            ;;
        debuglog)
            COMPREPLY=( $(compgen -W '-o --output --since -n --lines --help' -- "$cur") )
            ;;
        diag)
            COMPREPLY=( $(compgen -W '--help' -- "$cur") )
            ;;
        completion)
            COMPREPLY=( $(compgen -W 'bash zsh fish --help' -- "$cur") )
            ;;
        uninstall)
            COMPREPLY=( $(compgen -W '--purge --yes -y --help' -- "$cur") )
            ;;
        enable|disable)
            COMPREPLY=( $(compgen -W '--now --help' -- "$cur") )
            ;;
        status)
            COMPREPLY=( $(compgen -W '--verbose -v --help' -- "$cur") )
            ;;
        upgrade|restore)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W '--help' -- "$cur") )
            else
                COMPREPLY=( $(compgen -f -- "$cur") )
            fi
            ;;
        *)
            COMPREPLY=( $(compgen -W '--help' -- "$cur") )
            ;;
    esac
}
complete -F _kikimora_complete kikimora kk
