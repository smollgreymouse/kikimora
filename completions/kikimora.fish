function __kikimora_no_command
    not __fish_seen_subcommand_from install upgrade uninstall verify doctor backup restore start stop restart enable disable status interfaces dns config domains logs completion version help
end

complete -c kikimora -c kk -f
complete -c kikimora -c kk -n '__kikimora_no_command' -a install -d 'Установить или переустановить Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a upgrade -d 'Обновить Kikimora из локального пакета'
complete -c kikimora -c kk -n '__kikimora_no_command' -a uninstall -d 'Удалить Kikimora, сохранив Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a verify -d 'Проверить установку и конфигурацию'
complete -c kikimora -c kk -n '__kikimora_no_command' -a doctor -d 'Запустить расширенную диагностику'
complete -c kikimora -c kk -n '__kikimora_no_command' -a backup -d 'Создать резервную копию'
complete -c kikimora -c kk -n '__kikimora_no_command' -a restore -d 'Восстановить резервную копию'
complete -c kikimora -c kk -n '__kikimora_no_command' -a start -d 'Запустить Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a stop -d 'Остановить Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a restart -d 'Перезапустить Leshy'
complete -c kikimora -c kk -n '__kikimora_no_command' -a status -d 'Показать состояние Leshy и DNS'
complete -c kikimora -c kk -n '__kikimora_no_command' -a dns -d 'Управление системным DNS'
complete -c kikimora -c kk -n '__kikimora_no_command' -a config -d 'Управление конфигурацией'
complete -c kikimora -c kk -n '__kikimora_no_command' -a logs -d 'Показать журналы'
complete -c kikimora -c kk -n '__kikimora_no_command' -a completion -d 'Вывести сценарий автодополнения'
complete -c kikimora -c kk -n '__kikimora_no_command' -a version -d 'Показать версию'
complete -c kikimora -c kk -n '__kikimora_no_command' -a help -d 'Показать справку'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from dns' -a 'enable disable suspend resume status help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from config' -a 'show edit validate help'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l primary-interface -r -d 'VPN-интерфейс высокого приоритета'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l secondary-interface -r -d 'VPN-интерфейс низкого приоритета'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l non-interactive -d 'Не задавать интерактивных вопросов'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from logs' -s f -l follow -d 'Следить за журналом'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -l stop-services -d 'Разрешить остановку служб Leshy'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from install' -s y -l yes -d 'Согласиться без вопроса'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from uninstall' -l purge -d 'Удалить конфиги и резервные копии'
complete -c kikimora -c kk -n '__fish_seen_subcommand_from uninstall' -s y -l yes -d 'Согласиться без вопроса'

complete -c kikimora -c kk -n '__fish_seen_subcommand_from domains' -a 'status list add remove edit import export default direct primary secondary none help'
