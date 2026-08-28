_kikimora_complete() {
    local cur prev command subcommand
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    command="${COMP_WORDS[1]:-}"
    subcommand="${COMP_WORDS[2]:-}"

    if (( COMP_CWORD == 1 )); then
        COMPREPLY=( $(compgen -W 'install upgrade uninstall verify doctor debuglog diag backup restore start stop restart enable disable status interfaces dns config profiles domains routes logs completion version help --version -V --help -h' -- "$cur") )
        return
    fi

    case "$command" in
        dns)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W 'status enable disable suspend resume help -h --help' -- "$cur") )
            fi
            ;;
        profiles)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W 'list status add use remove help -h --help' -- "$cur") )
                return
            fi
            if [[ "$subcommand" == add ]]; then
                case "$prev" in
                    --primary-provider|--secondary-provider)
                        COMPREPLY=( $(compgen -W 'static happ command' -- "$cur") )
                        ;;
                    --primary-provider-args|--secondary-provider-args)
                        COMPREPLY=( $(compgen -f -- "$cur") )
                        ;;
                    *)
                        [[ "$cur" == -* ]] && COMPREPLY=( $(compgen -W '--primary-provider --secondary-provider --primary-provider-args --secondary-provider-args' -- "$cur") )
                        ;;
                esac
            fi
            ;;
        domains)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W 'status list add remove edit import export default help -h --help' -- "$cur") )
                return
            fi
            case "$subcommand" in
                list|edit|export)
                    (( COMP_CWORD == 3 )) && COMPREPLY=( $(compgen -W 'primary secondary bypass' -- "$cur") )
                    ;;
                add|remove)
                    (( COMP_CWORD >= 4 )) && COMPREPLY=( $(compgen -W '--primary --secondary --bypass' -- "$cur") )
                    ;;
                import)
                    if (( COMP_CWORD == 3 )); then
                        COMPREPLY=( $(compgen -f -- "$cur") )
                    elif (( COMP_CWORD == 4 )); then
                        COMPREPLY=( $(compgen -W 'primary secondary bypass' -- "$cur") )
                    fi
                    ;;
                default)
                    (( COMP_CWORD == 3 )) && COMPREPLY=( $(compgen -W 'direct primary secondary none' -- "$cur") )
                    ;;
            esac
            ;;
        routes)
            if (( COMP_CWORD == 2 )); then
                COMPREPLY=( $(compgen -W 'status list add remove edit import export help -h --help' -- "$cur") )
                return
            fi
            case "$subcommand" in
                list|edit|export)
                    (( COMP_CWORD == 3 )) && COMPREPLY=( $(compgen -W 'primary secondary' -- "$cur") )
                    ;;
                add|remove)
                    (( COMP_CWORD >= 4 )) && COMPREPLY=( $(compgen -W '--primary --secondary' -- "$cur") )
                    ;;
                import)
                    if (( COMP_CWORD == 3 )); then
                        COMPREPLY=( $(compgen -f -- "$cur") )
                    elif (( COMP_CWORD == 4 )); then
                        COMPREPLY=( $(compgen -W 'primary secondary' -- "$cur") )
                    fi
                    ;;
            esac
            ;;
        config)
            (( COMP_CWORD == 2 )) && COMPREPLY=( $(compgen -W 'show edit validate help -h --help' -- "$cur") )
            ;;
        install)
            case "$prev" in
                --leshy-binary) COMPREPLY=( $(compgen -f -- "$cur") ) ;;
                --primary-interface|--secondary-interface) COMPREPLY=() ;;
                *) COMPREPLY=( $(compgen -W '--primary-interface --secondary-interface --leshy-binary --non-interactive --stop-services --yes -y -h --help' -- "$cur") ) ;;
            esac
            ;;
        logs)
            COMPREPLY=( $(compgen -W '-f --follow --no-follow --all -n --lines -h --help' -- "$cur") )
            ;;
        debuglog)
            case "$prev" in
                -o|--output) COMPREPLY=( $(compgen -f -- "$cur") ) ;;
                --since|-n|--lines) COMPREPLY=() ;;
                *) COMPREPLY=( $(compgen -W '-o --output --since -n --lines -h --help' -- "$cur") ) ;;
            esac
            ;;
        diag)
            mapfile -t COMPREPLY < <(compgen -W '-h --help' -- "$cur")
            ;;
        completion)
            (( COMP_CWORD == 2 )) && COMPREPLY=( $(compgen -W 'bash zsh fish -h --help' -- "$cur") )
            ;;
        uninstall)
            COMPREPLY=( $(compgen -W '--purge --yes -y -h --help' -- "$cur") )
            ;;
        enable)
            COMPREPLY=( $(compgen -W '--now -h --help' -- "$cur") )
            ;;
        disable)
            COMPREPLY=( $(compgen -W '--now --force -h --help' -- "$cur") )
            ;;
        stop)
            COMPREPLY=( $(compgen -W '--force -h --help' -- "$cur") )
            ;;
        status)
            COMPREPLY=( $(compgen -W '--verbose -v -h --help' -- "$cur") )
            ;;
        upgrade)
            if (( COMP_CWORD == 2 )); then
                if [[ "$cur" == -* ]]; then
                    COMPREPLY=( $(compgen -W '-h --help' -- "$cur") )
                else
                    COMPREPLY=( $(compgen -f -- "$cur") )
                fi
            else
                case "$prev" in
                    --leshy-binary) COMPREPLY=( $(compgen -f -- "$cur") ) ;;
                    --primary-interface|--secondary-interface) COMPREPLY=() ;;
                    *) COMPREPLY=( $(compgen -W '--primary-interface --secondary-interface --leshy-binary --non-interactive --stop-services --yes -y -h --help' -- "$cur") ) ;;
                esac
            fi
            ;;
        restore)
            if (( COMP_CWORD == 2 )) && [[ "$cur" != -* ]]; then
                COMPREPLY=( $(compgen -f -- "$cur") )
            else
                COMPREPLY=( $(compgen -W '-h --help' -- "$cur") )
            fi
            ;;
        *)
            COMPREPLY=( $(compgen -W '-h --help' -- "$cur") )
            ;;
    esac
}
complete -F _kikimora_complete kikimora kk
