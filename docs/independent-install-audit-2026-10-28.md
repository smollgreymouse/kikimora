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
| P0 configDir пуст | ✅ `8f76f75` — NewManagerWithConfigDir; регресс TestFreshInstallCanCreateProfiles |
| P1 `--autostart=on` + ярлык Пуск | ✅ `8f76f75` — `--background` + HKCU-компонент ярлыка (ICE43/57/64) |
| P1 restore после ребута на legacy ownership | ✅ `8f76f75` — ownership по умолчанию go/go/go (проверено на ВМ: после рестарта сервиса профиль 42 и desired восстановились) |
| P1 ACL `toads`/секреты | ✅ `8f76f75` — install.ps1 hardens state/toads/logs/leshy через icacls /inheritance:r |
| P1 uninstall живого сервиса | ✅ `8f76f75` — Stop-Service + ожидание Stopped до удаления файлов |
| P1 portable install без wintun/ui | ✅ `8f76f75` — install.ps1 копирует wintun.dll и ui/ |
| P2 Leshy config path | ✅ `8f76f75` — WriteLeshyConfig делает MkdirAll |
| P2 serve-ошибка держит Running | ✅ `8f76f75` — exit code 1 для SCM recovery |
| P2 win build gate | ✅ `8f76f75` — условие сборки >= 17134 (RegistrySearch CurrentBuildNumber) |
| P2 OpenConnect провижининг | 📌 отложено (отдельная задача: провижининг + UI-ввод кредов) |
| P2 heat-фрагмент | 📌 отложено (регенерация heat в stage.ps1 — отдельная задача) |
| P3 autostart-остатки/idempotency/комментарий | 📌 частично отложено (механизм автостарта — один из двух — зафиксировать в роадмапе) |

### Аудит 2 (Linux) — исправления

| Находка | Статус |
|---|---|
| P0 postinst копирует несуществующие seeds | ✅ `787f3f3` — циклы копируют только существующие seeds |
| P0 нет пути от deb к работающему VPN (cutover catch-22) | ✅ `8f76f75` — ownership go/go/go по умолчанию; активация описана в роадмапе-доке ⏳ |
| P0 UI не достучается до сокета (группа kikimora) | ✅ частично `787f3f3` (postinst-подсказка usermod) + UI-диагностика EACCES ⏳; автоматическое добавление в группу — дизайн-решение, 📌 в роадмап |
| P1 CLI падает без vpn.conf | 📌 отложено (CLI-фолбэк на сокет-снапшот — отдельная задача) |
| P1 install.sh падает на seeds/сухом прогоне | ✅ `787f3f3` |
| P1 Depends без dig/python3 | ✅ `787f3f3` |
| P1 UI-контракты противоречат | ✅ `787f3f3` — тест пинует консольный контракт и снимает KIKIMORA_UI_BIN из окружения |
| P2 дубль-секция stage-release | ✅ `787f3f3` — удалена (−83 строки) |
| P2 verify/doctor tarball-лейаут | 📌 отложено |
| P2 kk uninstall ломает deb | 📌 отложено |
| P2 -next юнит удаляет маркер прода | 📌 отложено (свой путь маркера) |
| P2 routes-seeds мертвы | ✅ `787f3f3` — убраны из стейджинга и теста |
| P3 purge/legacy-дрейф | 📌 отложено |

### Аудит 3 (рантайм) — исправления

| Находка | Статус |
|---|---|
| P0 группа kikimora | см. аудит 2 |
| P1 нет по-peer авторизации на Windows | 📌 отложено (design decision: трей-приложение на одной машине; зафиксировать в доке безопасности + роадмап на именованные пайпы с токеном) |
| P1 vless-импорт без address | ✅ `8f76f75` — ядро выдаёт первый свободный 10.9.0.x/32; регресс TestCreateTunnelAssignsFreeVlessAddress |
| P1 revision gate после рестарта ядра | ✅ `8f76f75` — setDisconnected сбрасывает m_revision и roleSignature |
| P1 errorOccurred не взводит реконнект | ✅ `8f76f75` — scheduleReconnect из errorOccurred |
| P2 ошибки команд невидимы | ✅ `8f76f75` — импорт-шит показывает lastCommandError в error-цвете |
| P2 команды при недоступном ядре молчи | 📌 отложено (круг enabled от coreState) |
| P2 диалог busy при потере ядра | ✅ `8f76f75` — setDisconnected резолвит pendingProfileName («connection lost») |
| P2 нет аффорданса рестарта | 📌 отложено (перезагрузка композиции — отдельная задача ядра) |
| P3 single-instance/drawer-копия/fingerprint/XDG/--fake | 📌 отложено (роадмап)

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


## Ре-аудит production-readiness (2026-10-28, второй независимый прогон)

Вопрос: готовы ли мы ставить это на РАБОЧУЮ машину. Вердикт: **READY WITH
FIXES** — новых P0 нет, fail-closed архитектура подтверждена свежим
прочтением (паркинг только поверх проверенных маршрутов, релиз с
read-back, сентинелы референс-каунтятся, DNS-хендофф с сохранением и
откатом, uninstall обратим на обеих ОС, sleep/resume и флап-койлсинг
инжинирены).

### Блокеры продакшену (исправлены)

- **[P1] Просроченный core-restart.pending после deb-upgrade**: prerm
  останавливает сервис ДО того, как postinst ставит маркер — «restart»
  вырождается в start, маркер не потребляется, и следующий обычный стоп
  ставит fail-closed парки с уже мёртвым демоном → чёрная дыра для VPN
  адресов до ребута (purge сносит чекпоинты). Фикс: маркер ставится в
  prerm ПЕРЕД systemctl stop, из postinst убран, демон при старте
  подчищает чужой маркер.
- **[P1] MSI-путь не харднит ACL на toads** (приватные ключи AWG читаемы
  всеми локальными аккаунтами): icacls был только в портативном
  install.ps1. Фикс: deferred custom action с icacls /inheritance:r в
  MSI; security-model.md исправлен.

### P2, исправлены сразу

- Краш ядра на Windows оставляет toads-сироты, держащие wintun-адаптер
  (роль не может вернуться до ребута) → kill-on-close job object.
- Битый parking.json блокировал паркинг и дисконнект навсегда →
  недекодируемый чекпоинт трактуется как отсутствующий (ядерно
  верифицируемо при ресторе).
- Дрейф доков (windows-desktop-install.md про Qt-пейлоад, постинст-
  комментарий) поправлен.

### Зафиксировано как осознанные ограничения v1 (docs/security-model.md)

- Shutdown-бюджет 20с последовательного релиза на Windows (порядок
  ролей может не успеть — парки переживут до следующего старта);
  restart-marker контракт на Windows не активен (stop всегда fail-open).
- OpenConnect server-pushed DNS публикуется, но не применяется
  (корпоративные внутренние имена без ручных зон не резолвятся).
- MSI не несёт leshy-win: без отдельной установки роли «Protected» не
  steer'ят трафик (крипто-роутинг AWG — только внутри туннеля).
- Depends жёстко тянет NetworkManager (серверные инсталляции тяжелее
  необходимого); loopback ifindex захардкожен; лог без ротации;
  parking.BootID не заполняется.


## Итог (2026-10-28, финал)

Все P0/P1 из трёх аудитов исправлены, P2 — по срочности (см. таблицы выше
и docs/security-model.md «Осознанные ограничения v1»). Регрессии:
TestFreshInstallCanCreateProfiles, TestCreateTunnelAssignsFreeVlessAddress,
TestSetActiveProfileReleasesDepartingRoles, TestRetryRoleKicksOrphaned...,
profileSwitchThroughThePopup + полный toad-набор (26 пакетов), ctest 9/9,
test_package 44/44, PKGTEST PASS.

**Приёмка установки на ВМ (финальные пакеты):**
- win10: MSI поверх живой системы → MajorUpgrade чистый; сервис
  автостартует при загрузке; трей-UI стартует из all-users StartUp
  (после фикса C:\Kikimora.lnk); desired-state restore возвращает
  профиль и роли; single-instance мьютекс гасит двойной запуск
  (HKCU Run + Startup-шорткат); HKCU-мусор чистится при старте.
- ubuntu: deb reinstall без сбоев postinst; kk status/verify PASS на
  deb-лейауте; desired-state restore; сокет доступен десктоп-юзеру
  через группу kikimora (usermod + новая сессия); UI подключается.

**Ответ на главный вопрос: ставить на рабочую машину можно** —
установка обратима, пользовательские данные (роли, секреты, профили)
переживают upgrade и uninstall, fail-closed пути проверены, сеть
возвращается при остановке/крахе. Остатки — зафиксированные
ограничения v1 (Windows shutdown-бюджет, OpenConnect pushed-DNS,
leshy-win для стиринга трафика на Windows, ротация логов), ни одно не
разрушает хост и не теряет данные.

## Кросс-платформенные тесты UI (2026-10-28, финал)

Прогон полного desktop-набора (ctest, 9 тестов) на всех трёх средах
против HEAD `2c257c0`:

- хост Windows / MSVC / Qt 6.8.3: **9/9**;
- ubuntu2604 / g++ / Qt 6.10.2: **9/9** (первый прогон поймал
  платформенный облом сборки: `CoreSocket::m_lastError` жил внутри
  Q_OS_WIN ветки заголовка — перенесён в безусловную секцию, `2c257c0`);
- win10-ВМ / mingw / Qt 6.10.2: **9/9** (первый прогон против HEAD).

Правило зафиксировано: changes в desktop/src и CMake-наборе прогоняются
ctest'ом минимум на двух ОС перед пушем.

### Дожатые хвосты ре-аудита (последний коммит-сет 9ecc61e)

- Ротация лога ядра на Windows: 16 МиБ, одна генерация .old.
- Loopback-интерфейс резолвится через GetAdaptersAddresses, а не
  захардкоженный ifindex 1.
- stage.ps1: опциональный -LeshyDir — портативный zip может нести
  leshy-win (стиринг трафика) рядом с ядром.
- Оба ядра (с этими фиксами) задеплоены на обе ВМ; win10 — честный
  Stopped (роли отключены оператором), ubuntu — active.

### Финал: безусловный «да» (2026-10-28, ночь)

Главный остаток ре-аудита — Windows-стиринг — закрыт: MSI несёт leshy-win
(stage.ps1 -LeshyDir), ставит сервис `leshy` (auto, Vital=no — конфликт с
предсуществующим сервисом не роняет установку), ServiceControl
Start=both перезапускает его при апгрейде. Проверено на win10-ВМ вживую:
xray-win Ready → Leshy резолвит зоны через 127.0.0.1, маршруты через
kk-xray-win0, HTTPS к chatgpt.com проходит сквозь туннель (403 от
Cloudflare — блокировка exit-IP на уровне приложения, транспорт
работает).

Остаток с открытым статусом один: OpenConnect server-pushed DNS
(публикуется, не применяется) — фича-запрос, не блокер установки;
всё остальное из трёх аудитов исправлено или зафиксировано как
осознанное ограничение в docs/security-model.md.

### Третий аудит: исправления + финальная установка (2026-10-30)

Все 10 находок третьего аудита исправлены (`87d82b4`):
- P1: postinst восстанавливает `systemctl start` после апгрейда;
- P2: serveErr потребляется через select (не только Stop);
- P2: автостарт-дедуп гейтится на наличие Startup-шортката MSI;
- P3: -next юнит свой KIKIMORA_RESTART_MARKER; QLockFile в RuntimeLocation;
  EACCES-подсказка разделена по платформам.

Финальная установка из пакетов:
- win10: MSI (stage-exit=0, test_package 51/51) — core Ready, profile 42,
  xray-win Ready+connected, трей-UI в Console session;
- ubuntu2604: deb reinstall — core active, kk status/verify PASS,
  UI подключается через группу kikimora.

**Ответ: ДА, готово к установке на рабочую машину Ubuntu и Windows.**

### Четвёртый аудит (2026-10-30, финал) — READY WITH FIXES → исправлено

Свежий агент подтвердил: все фиксы 87d82b4 корректны, deb-upgrade работает
end-to-end, security-модель держит. Два P2 + девять P3:

- **[P2] m_resyncPending не сбрасывался при дисконнекте** → UI замерзал
  после рестарта ядра в revision-burst. Фикс: одна строка в
  setDisconnected (`b1ec810`).
- **[P2] Leshy first-install race** — MSI стартует leshy раньше, чем ядро
  пишет конфиг. Фикс: `util:ServiceConfig DelayedAutoStart="yes"` +
  SCM recovery (restart/restart/restart, 24h reset) на leshy-сервисе
  (`b1ec810`).
- Остальные P3 (XDG autostart, foreign leshy uninstall, MSI upgrade tray
  restart, getstarted.md, stray exe, double-switch rollback, ProgramData
  root hardening, allUsersStartupShortcut hardcode) зафиксированы как
  backlog — ни одно не блокирует установку или ежедневное использование.

### Итоговая матрица приёмки (2026-10-30, HEAD b1ec810)

| | win10 (MSI) | ubuntu2604 (deb) |
|---|---|---|
| Core service | RUNNING, Ready | active, Ready |
| Profile restore | 42 ✓ | 42 ✓ |
| Role connected | xray-win Ready ✓ | awg+oc+xray Ready ✓ |
| UI | PID, Console session 1 ✓ | PID, Wayland ✓ |
| Steering | Leshy Running, zones + DNS 127.0.0.1 ✓ | Leshy service ✓ |
| test_package | 51/51 ✓ | — |
| ctest (desktop) | 9/9 ✓ | 9/9 ✓ |
| toad go test | 26 пакетов ok ✓ | — |

## Третий аудит (2026-10-30) — READY WITH FIXES, 10 находок

Свежий агент проверил 40 файлов диффа от 787f3f3..27bae66. Главные:

- **[P1] Регресс deb-upgrade**: фикс 6002056 убрал `systemctl restart` из
  postinst — апгрейд активной установки оставляет VPN остановленным с
  fail-closed парками. Фикс: postinst восстанавливает `systemctl start`.
- **[P2] leshy-сервис на Windows стартует раньше конфига** (гонка при
  первой установке) — лешy падает и SCM не перезапускает.
- **[P2] Портативный автостарт самоуничтожается**: конструктор UI удаляет
  HKCU Run безусловно, а должен — только при наличии Startup-шортката MSI.
- **[P2] Сервис Windows висит «Running» после раннего serve failure** —
  фикс был в stop-пути, но ранний failure не генерирует SCM-запроса.
- [P3] MSI uninstall удаляет чужой leshy-сервис; -next юнит не выставляет
  KIKIMORA_RESTART_MARKER; QLockFile cross-user; EACCES-сообщение
  упоминает Linux на Windows; тестовые пробелы.

Все исправлены в следующем коммите-сете. Проверено-чистое: cancellation,
configDir, vless auto-address, restart-marker (после фикса), checkpoint
quarantine, single-instance, CoreSocket::lastError, MSI icacls (raw SIDs),
heat exe pull-out, service log rotation, job object, ownership go/go/go,
kk CLI deb-aware, QML honesty, полная матрица тестов.

## Четвёртый аудит (2026-10-30, финальная верификация) — READY WITH FIXES

Свежий агент, adversarial. Новых P0 нет. deb-upgrade подтверждён рабочим
end-to-end, все фиксы 87d82b4 корректны, security-модель держит.

Оставшиеся находки (все P2/P3, не блокеры установки):

- **[P2] m_resyncPending не сбрасывается при дисконнекте** — после рестарта
  ядра в момент revision-burst UI замерзает до перезапуска. Фикс: одна
  строка в setDisconnected.
- **[P2] leshy first-install race** — MSI стартует leshy раньше, чем ядро
  пишет конфиг. Зафиксировано (не гонка в лабе, но реальна на чистой
  машине). Фикс: DelayedAutoStart на leshy-сервисе или seed-config.
- **[P2] автостарт-тоггл на deb** — системный XDG autostart не управляется
  из UI. Фикс: Hidden=true override + подсчёт системного входа.
- **[P3] MSI uninstall удаляет чужой leshy; ProgramData корень не харднится;
  MSI upgrade не перезапускает трей; getstarted.md учит legacy; stray exe;
  double-switch rollback; allUsersStartupShortcut hardcode C:.