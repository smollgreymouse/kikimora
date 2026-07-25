function __kikimora_no_command
    not __fish_seen_subcommand_from install upgrade uninstall verify doctor backup restore start stop restart enable disable status interfaces dns config domains logs completion version help
end

complete -c kikimora -c kk -f
complete -c kikimora -c kk -n '__kikimora_no_command' -a install -d 'Install or reinstall Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a upgrade -d 'Upgrade Kikimora from a local package'
complete -c kikimora -c kk -n '__kikimora_no_command' -a uninstall -d 'Remove Kikimora while keeping Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a verify -d 'Verify installation and configuration'
complete -c kikimora -c kk -n '__kikimora_no_command' -a doctor -d 'Run extended diagnostics'
complete -c kikimora -c kk -n '__kikimora_no_command' -a backup -d 'Create a backup'
complete -c kikimora -c kk -n '__kikimora_no_command' -a restore -d 'Restore from a backup'
complete -c kikimora -c kk -n '__kikimora_no_command' -a start -d 'Start Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a stop -d 'Stop Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a restart -d 'Restart Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a status -d 'Show Leshy and DNS state'
complete -c kikimora -c kk -n '__kikimora_no_command' -a dns -d 'Manage system DNS'
complete -c kikimora -c kk -n '__kikimora_no_command' -a config -d 'Manage configuration'
complete -c kikimora -c kk -n '__kikimora_no_command' -a logs -d 'Show logs'
complete -c kikimora -c kk -n '__kikimora_no_command' -a completion -d 'Generate completion script'
complete -c kikimora -c kk -n '__kikimora_no_command' -a version -d 'Show version'
complete -c kikimora -c kk -n '__kikimora_no_command' -a help -d 'Show help'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from dns' -a 'enable disable suspend resume status help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from config' -a 'show edit validate help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l primary-interface -r -d 'Higher-priority VPN interface'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l secondary-interface -r -d 'Lower-priority VPN interface'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l non-interactive -d 'Do not ask interactive questions'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from logs' -s f -l follow -d 'Follow the log'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l stop-services -d 'Allow stopping Leshy services'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -s y -l yes -d 'Agree without prompting'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from uninstall' -l purge -d 'Remove configs and backups'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from uninstall' -s y -l yes -d 'Agree without prompting'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from domains' -a 'status list add remove edit import export default direct primary secondary none help'
