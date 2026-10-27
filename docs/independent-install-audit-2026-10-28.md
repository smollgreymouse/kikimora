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

⏳ аудитор перезапущен, результаты будут добавлены.

## Аудит 3: рантайм ядра↔UI (авторизация API, гонки, честные состояния)

⏳ аудитор перезапущен, результаты будут добавлены.
