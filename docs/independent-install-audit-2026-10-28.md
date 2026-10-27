# Независимый аудит «ставится и работает?» — 2026-10-28

Три агента с пустым контекстом, код ветки `feat/amnezia-inspired-ui`.
Главный вопрос: **можно ли ставить продукт в систему и пользоваться на
Ubuntu и Windows?** Находки фиксируются по мере поступления; статусы
исправлений обновляются в конце файла.

## Аудит 1: Windows (MSI → сервис → трей → ежедневное использование)

### Вердикт: NOT READY

MSI сам по себе корректен (stage → компоненты с стабильными GUID →
MajorUpgrade → ServiceControl → ProgramData переживает upgrade/uninstall).
Но «пользоваться» из коробки нельзя:

- **[P0] Свежая установка не может создавать профили и туннели.**
  `toad/cmd/kikimora-core/main.go:80-88,151` строит manager только из
  найденных конфигов; на чистой установке `C:\ProgramData\Kikimora\toads`
  пуст → `configDir == ""` (`control.go:196-201`) → каждый CreateTunnel /
  CreateProfile / SetTunnelProfiles отклоняется («the core has no
  configuration directory»: `config_api.go:23,36,67,180`). Единственный
  путь создания ролей в UI мёртв.
- **[P1] Пост-инсталл запуск убивает UI; нет ярлыка в меню Пуск.**
  `kikimora.wxs:48-50` запускает `kikimora-ui.exe --background
  --autostart=on` — опции `--autostart` не существует
  (`desktop/src/main.cpp:51-65`), парсер роняет процесс; ярлыка
  ProgramMenuFolder нет (только автостарт-шорткат `kikimora.wxs:76-78`).
- **[P1] Туннели не возвращаются после ребута на дефолтном ownership.**
  Инсталлер кладёт `routing_owner="legacy"`, `tunnel_owner="external"`
  (`packaging/windows/files/ownership.conf:6-8`), а `main.go:204-220`
  восстанавливает desired-state только при go+go (`:218`) → сервис
  стартует, туннели — нет.
- **[P1] Непривилегированные пользователи читают секреты и исполняют код
  как SYSTEM через `toads`.** TOML-роли с приватными ключами пишутся с
  `0o600`, который на Windows игнорируется (`create.go:74`) → наследуется
  Users-ACE ProgramData; сервис грузит каждый `*.toml` (`main.go:38-55`),
  а `endpoint_policy.command` исполняется от SYSTEM
  (`config.go:50-55,346-359` → `endpoint/specs.go:137-143`).
- **[P1] Portable-uninstall не может удалить бинарники живого сервиса.**
  `uninstall.ps1:29-39` зовёт `service uninstall`, который только
  `Delete()` без остановки (`service_windows.go:239-248`), затем
  `Remove-Item` через 500мс падает на занятом файле.
- **[P1] Portable-install не ставит `wintun.dll` и ломает UI.**
  `install.ps1:34-40` копирует только exe; без wintun.dll рядом с
  `kikimora-toad.exe` ни один AWG-адаптер не поднимется; UI без
  Qt-рантайма в каталоге ядра не стартует.
- **[P2] «Усиленный» state-каталог не усилен; история авторизации сокета
  противоречива.** `util:PermissionEx` лишь добавляет ACE к наследуемым
  (`kikimora.wxs:121-128,136-145`); `transport_windows.go:66-100`
  сознательно даёт BUILTIN\Users RW на `core.sock`, а `peer_windows.go`
  комментирует обратное — локальный пользователь может вести весь
  control-plane (решение tray-приложения, но оно должно быть явным).
- **[P2] Запись Leshy-конфига падает на каждом старте и смене bypass.**
  `DefaultLeshyConfigPath()` = `C:\ProgramData\leshy\config.toml`
  (`leshy_zones.go:21-26`) — каталог никто не создаёт;
  `WriteLeshyConfig` пишет tmp без MkdirAll (`leshy_config.go:144-147`).
- **[P2] Сервис вечно «Running» после раннего падения serve.**
  `service_windows.go:124-144`: ошибка `runServe` читается только в stop
  -пути; SCM-recovery не срабатывает.
- **[P2] Инсталлер пускает Windows без AF_UNIX (< 17063).**
  `kikimora.wxs:62-64` (`VersionNT >= 603`), а сокет требует 17063+.
- **[P2] OpenConnect непровижинируем из коробки** (нет openconnect.exe,
  https-импорт не спрашивает username/password: `TunnelImport.cpp:102-112`
  против `create.go:151-157`).
- **[P2] Статический heat-фрагмент хрупок** — 3317 компонентов должны
  точно совпадать с `ui/` от deploy-ui.ps1; регенерации нет.
- **[P3] Autostart-остатки**: HKCU Run не чистится при uninstall;
  CommonStartup-шорткат побеждает тумблер в UI.
- **[P3] Повторный portable-install без uninstall падает** («already
  exists»), install.ps1 не идемпотентен.
- **[P3] Устаревший комментарий `stage.ps1:15`** («FakeCore on Windows
  today» — UI давно RealCoreClient).

Проверено-чистое: upgrade-путь MSI, wintun доезжает в Program Files,
xray инпроцессный, входящих слушателей нет (фаервол не нужен),
версии API сходятся.

### Исправления (обновляется по ходу)

| Находка | Статус |
|---|---|
| P0 configDir пуст | ⏳ в работе |
| P1 `--autostart=on` + ярлык Пуск | ⏳ |
| P1 restore после ребута на legacy ownership | ⏳ |
| P1 ACL `toads`/секреты | ⏳ |
| P1 uninstall живого сервиса | ⏳ |
| P1 portable install без wintun/ui | ⏳ |
| P2 Leshy config path | ⏳ |
| P2 serve-ошибка держит Running | ⏳ |
| P2 win build gate | ⏳ |
| P2 OpenConnect провижининг | ⏳ (осознанно: отдельная задача) |
| P2 heat-фрагмент | ⏳ (осознанно: задокументировать регенерацию) |
| P3 autostart/idempotency/комментарий | ⏳ |

## Аудит 2: Linux (deb → systemd → kk/UI → ежедневное использование)

### Вердикт: NOT READY

Свежий `apt install ./kikimora.deb` даже не донастраивается: postinst
копирует несуществующие seeds. За этим — отсутствует любой путь от
инертной установки к работающему продукту, а UI не может достучаться до
сокета никогда.

- **[P0] postinst падает на каждой свежей установке; deb остаётся
  unconfigured.** Сгенерированный postinst (`stage-release.sh:385-390`)
  циклит `primary.txt secondary.txt bypass.txt` и `cp`-ит каждый из
  `/usr/share/kikimora/leshy/domains`, но стейджится только `bypass.txt`
  (моя правка `1704ab7` почистила staging-сторону, но не postinst-сторону!)
  → первый `cp` роняет postinst при `set -e`. Фикс: циклить только
  `bypass.txt`.
- **[P0] Нет пути от свежего deb к работающему VPN.** (a) postinst
  сознательно не включает/не стартует ядро; документированная активация
  `kk orchestration cutover --go` умирает на пустом `/etc/kikimora/toads`
  (`orchestration.sh:90` — compgen die при нуле TOML), а TOML-ы не создаёт
  никто до работающего ядра — catch-22. (b) Все legacy-команды (`kk
  start/enable/status`) ссылаются на несуществующих вотчеров и `vpn.conf`.
- **[P0] Десктопный UI не может подключиться к ядру никогда.** Сокет
  0660 root:kikimora в /run/kikimora 0750 (`kikimora-core.service:20-21`);
  авторизация uid0/self/группа (`peer_linux.go:36-66`); никого в группу
  `kikimora` не добавляет никто → UI вечно «Unavailable».
- **[P1] CLI-команды статуса/профилей падают на deb-установке**
  (`common.sh:69` → source vpn.conf; `config.sh:49-52` die).
- **[P1] install.sh (tarball-путь) умирает посреди коммита на чистой
  системе** (копирует несуществующие primary/secondary: `install.sh:760-770`
  против `:909-910`).
- **[P1] DNS-интеграции нужны `dig` и `python3`, которых нет в Depends**
  (`leshy-dns:222,125`; `check-config:28-32`; Depends в `stage-release.sh:315`).
- **[P1] Два контракта об UI противоречат друг другу** (stage с KIKIMORA_UI_BIN
  против теста «UI не должен попадать в пакет» + Qt только в Recommends).
- **[P2] stage-release.sh содержит дублированную пре-leshy секцию**
  (строки 1-93 vs 131+: двойная сборка, правки первой половины молча
  неэффективны).
- **[P2] `kk verify`/`doctor` проверяют tarball-лейаут** → на deb всегда FAIL
  (`maintenance.sh:44-83`).
- **[P2] `kk uninstall` ломает deb-установку** (не останавливает сервис,
  удаляет чужое, оставляет своё).
- **[P2] Сайд-бай-сайд юнит удаляет маркер рестарта продакшн-ядра**
  (`kikimora-core-next.service:14`).
- **[P2] Route-seeds стейджатся туда, где их никто не читает**
  (`stage-release.sh:204-209` vs `common.sh:10,16-17`).
- **[P3] Purge сохраняет сгенерированный state**; [P3] legacy-дрейф в
  человекочитаемом выводе (route-watch/health-watch, diag-хост, kk watch help).

Проверено-чистое: wiring юнитов (Wants/PartOf/Before + drop-in), DNS
suspend/resume intent, upgrade-маркер prerm→postrm→postinst, авторизация
сокета и права 0660/0750, NM unmanaged-паттерн совпадает с именами
интерфейсов toad'ов, комплишены и symlink резолвятся.

## Аудит 3: рантайм ядра↔UI (авторизация API, гонки, честные состояния)

### Вердикт: NOT READY

- **[P0] Ubuntu: UI не может подключиться к сокету после стоковой
  установки** — root:kikimora 0750/0660, никого в группу kikimora не
  добавляет никто (дублирует находку аудита 2); UI показывает
  «Unavailable» без объяснения EACCES.
- **[P1] Windows: по-peer авторизации нет** — `allowInteractiveUsers`
  даёт BUILTIN\Users RW на core.sock, `authorizePeer` no-op → любой
  локальный процесс может DisconnectAll/RemoveTunnel/переписать роутинг
  (контроль, не disclosure: секретов в снапшоте нет).
- **[P1] vless:// импорт отвергается реальным ядром всегда** — спека без
  `address` (`TunnelImport.cpp:59-75`) против обязательного адреса
  (`create.go:125-126`); фейк маскирует поломку.
- **[P1] После рестарта ядра UI замерзает на устаревшем состоянии:**
  `m_revision` не сбрасывается при дисконнекте (`RealCoreClient.cpp:494,
  333-341`), а ревизия нового процесса начинается с 1 → все снапшоты
  отбрасываются, пока ревизия не догонит (минуты «Protected» на мёртвом ядре).
- **[P1] Реконнект-луп не взводится на неудачном диале** — retry-лестница
  только в `disconnected`; `errorOccurred` (UI стартует раньше ядра!)
  оставляет «Unavailable» навсегда.
- **[P2] Ошибки команд невидимы в нужных местах** (import-шит показывает
  только message; lastCommandError — лишь в диагностике).
- **[P2] Команды при недоступном ядре молча роняются** (круг всегда
  кликабелен, трей — нет: поверхности расходятся).
- **[P2] Диалог создания профиля навсегда busy, если ядро упало до ответа**
  (`m_pendingProfileName` не резолвится при дисконнекте).
- **[P2] Create/RemoveTunnel без аффорданса рестарта** (restart-only
  подтверждён, но в UI нет сигнала «перезапустите» кроме мессаги).
- **[P3] Нет single-instance guard; [P3] drawer держит замороженную копию
  роли; [P3] fingerprint стрима не включает underlay/parking; [P3] XDG
  Exec без кавычек и --fake в релизном бинарнике.
