function __kikimora_no_command
    not __fish_seen_subcommand_from install upgrade uninstall verify doctor debuglog diag backup restore start stop restart enable disable status interfaces dns config profiles domains routes logs completion version help
end

function __kikimora_needs_nested_command -a parent
    set -l tokens (commandline -opc)
    test (count $tokens) -eq 2; and test "$tokens[2]" = "$parent"
end

function __kikimora_nested_command_is -a parent child
    set -l tokens (commandline -opc)
    test (count $tokens) -ge 3; and test "$tokens[2]" = "$parent"; and test "$tokens[3]" = "$child"
end

complete -c kikimora -c kk -f
complete -c kikimora -c kk -n '__kikimora_no_command' -a install -d 'Install or reinstall Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a upgrade -d 'Upgrade Kikimora from a local package'
complete -c kikimora -c kk -n '__kikimora_no_command' -a uninstall -d 'Remove Kikimora while keeping Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a verify -d 'Verify installation and configuration'
complete -c kikimora -c kk -n '__kikimora_no_command' -a doctor -d 'Run extended diagnostics'
complete -c kikimora -c kk -n '__kikimora_no_command' -a debuglog -d 'Write a Linux debug log bundle'
complete -c kikimora -c kk -n '__kikimora_no_command' -a diag -d 'Capture focused secondary-VPN diagnostics'
complete -c kikimora -c kk -n '__kikimora_no_command' -a backup -d 'Create a backup'
complete -c kikimora -c kk -n '__kikimora_no_command' -a restore -d 'Restore from a backup'
complete -c kikimora -c kk -n '__kikimora_no_command' -a start -d 'Start Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a stop -d 'Stop Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a restart -d 'Restart Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a enable -d 'Enable service autostart'
complete -c kikimora -c kk -n '__kikimora_no_command' -a disable -d 'Disable service autostart'
complete -c kikimora -c kk -n '__kikimora_no_command' -a status -d 'Show Leshy and DNS state'
complete -c kikimora -c kk -n '__kikimora_no_command' -a interfaces -d 'Show interfaces, addresses and routes'
complete -c kikimora -c kk -n '__kikimora_no_command' -a dns -d 'Manage system DNS'
complete -c kikimora -c kk -n '__kikimora_no_command' -a config -d 'Manage configuration'
complete -c kikimora -c kk -n '__kikimora_no_command' -a profiles -d 'Manage VPN interfaces and endpoint providers'
complete -c kikimora -c kk -n '__kikimora_no_command' -a domains -d 'Manage domain lists'
complete -c kikimora -c kk -n '__kikimora_no_command' -a routes -d 'Manage static IP/CIDR route lists'
complete -c kikimora -c kk -n '__kikimora_no_command' -a logs -d 'Show logs'
complete -c kikimora -c kk -n '__kikimora_no_command' -a completion -d 'Generate completion script'
complete -c kikimora -c kk -n '__kikimora_no_command' -a version -d 'Show version'
complete -c kikimora -c kk -n '__kikimora_no_command' -a help -d 'Show help'
complete -c kikimora -c kk -n '__kikimora_no_command' -s V -l version -d 'Show version'
complete -c kikimora -c kk -n '__kikimora_no_command' -s h -l help -d 'Show help'

complete -c kikimora -c kk -n '__kikimora_needs_nested_command dns' -a 'status enable disable suspend resume help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from dns' -s h -l help -d 'Show help'

complete -c kikimora -c kk -n '__kikimora_needs_nested_command profiles' -a 'list status add use remove help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from profiles' -s h -l help -d 'Show help'
complete -c kikimora -c kk -n '__kikimora_nested_command_is profiles add' -l primary-provider -r -a 'static happ command' -d 'Primary endpoint provider'
complete -c kikimora -c kk -n '__kikimora_nested_command_is profiles add' -l secondary-provider -r -a 'static happ command' -d 'Secondary endpoint provider'
complete -c kikimora -c kk -n '__kikimora_nested_command_is profiles add' -l primary-provider-args -r -d 'Primary endpoint provider argument'
complete -c kikimora -c kk -n '__kikimora_nested_command_is profiles add' -l secondary-provider-args -r -d 'Secondary endpoint provider argument'

complete -c kikimora -c kk -n '__kikimora_needs_nested_command config' -a 'show edit validate help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from config' -s h -l help -d 'Show help'

complete -c kikimora -c kk -n '__kikimora_needs_nested_command domains' -a 'status list add remove edit import export default help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from domains' -s h -l help -d 'Show help'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains list' -a 'primary secondary bypass'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains edit' -a 'primary secondary bypass'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains export' -a 'primary secondary bypass'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains add' -l primary -d 'Use primary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains add' -l secondary -d 'Use secondary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains add' -l bypass -d 'Use bypass zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains remove' -l primary -d 'Use primary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains remove' -l secondary -d 'Use secondary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains remove' -l bypass -d 'Use bypass zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains import' -F
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains import' -a 'primary secondary bypass'
complete -c kikimora -c kk -n '__kikimora_nested_command_is domains default' -a 'direct primary secondary none'

complete -c kikimora -c kk -n '__kikimora_needs_nested_command routes' -a 'status list add remove edit import export help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from routes' -s h -l help -d 'Show help'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes list' -a 'primary secondary'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes edit' -a 'primary secondary'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes export' -a 'primary secondary'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes add' -l primary -d 'Use primary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes add' -l secondary -d 'Use secondary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes remove' -l primary -d 'Use primary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes remove' -l secondary -d 'Use secondary zone'
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes import' -F
complete -c kikimora -c kk -n '__kikimora_nested_command_is routes import' -a 'primary secondary'

complete -c kikimora -c kk -n '__kikimora_needs_nested_command completion' -a 'bash zsh fish'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from completion' -s h -l help -d 'Show help'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from enable' -l now -d 'Enable and start immediately'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from disable' -l now -d 'Disable and stop immediately'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from disable' -l force -d 'Force endpoint-policy clear while VPNs are up; requires --now'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from stop' -l force -d 'Force endpoint-policy clear while VPNs are up'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from status' -l verbose -s v -d 'Verbose output'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l primary-interface -r -d 'Higher-priority VPN interface'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l secondary-interface -r -d 'Lower-priority VPN interface'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l leshy-binary -r -F -d 'Use Leshy binary from path'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l non-interactive -d 'Do not ask interactive questions'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l stop-services -d 'Allow stopping Leshy services'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -s y -l yes -d 'Agree without prompting'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -F -d 'ZIP file or directory path'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -l primary-interface -r -d 'Higher-priority VPN interface'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -l secondary-interface -r -d 'Lower-priority VPN interface'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -l leshy-binary -r -F -d 'Use Leshy binary from path'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -l non-interactive -d 'Do not ask interactive questions'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -l stop-services -d 'Allow stopping Leshy services'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from upgrade' -s y -l yes -d 'Agree without prompting'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from restore' -F -d 'Backup archive path'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from uninstall' -l purge -d 'Remove configs and backups'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from uninstall' -s y -l yes -d 'Agree without prompting'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from logs' -s f -l follow -d 'Follow the log'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from logs' -l no-follow -d 'Do not follow log output'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from logs' -l all -d 'Show all logs'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from logs' -l lines -s n -r -d 'Number of log lines'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from debuglog' -l output -s o -r -F -d 'Write debug bundle to path'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from debuglog' -l since -r -d 'Use journalctl --since time expression'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from debuglog' -l lines -s n -r -d 'Limit journal output'

complete -c kikimora -c kk -n 'not __kikimora_no_command' -s h -l help -d 'Show help'
