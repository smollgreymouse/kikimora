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

### Финальная переустановка после всех исправлений (2026-10-30, 23:00)

MSI пересобран из HEAD (`98df38a`), переустановлен на win10-ВМ. После
установки: core Ready/42, xray-win Ready+connected, **leshy RUNNING**
(чистая установка из MSI — первый раз, где leshy-сервис работает из
пакета без ручного шага), DNS chatgpt.com резолвится через 127.0.0.1
(Leshy), трей-UI запущен. ubuntu2604: deb reinstall → core Ready/42,
все роли Ready. Обе ВМ работают из финальных пакетов.

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
## Chaos/resilience test (2026-10-30, финал) — проверка отказоустойчивости

### Ubuntu2604 — все 4 теста PASS

1. **Сеть down/up**: `ip link set enp0s3 down` → все роли → `WaitingForUnderlay`
   (fail-closed), up → все роли `Ready` за 8 секунд. Сеть возвращается.
2. **kill -9 ядра**: systemd Restart=on-failure перезапускает за 10с,
   все 3 роли `Ready` за 18с.
3. **Stop с ролями → start**: парки установлены на стопе (0 остаточных
   после рестарта), все 3 роли `Ready`.
4. **Disconnect all → reconnect all**: 0 owned routes после дисконнекта,
   все роли `Ready` после реконнекта.

### win10 — ядро адаптивно (адаптер убивает SSH)

Адаптер disable/enp0s3 kill SSH (та же сетевая карта). Остальные:
- Core stop→start: Running после рестарта, xray-win Ready+connected;
- kill -9: SCM recovery перезапускает, роли восстанавливаются;
- Leshy: Running после MSI (Vital=no + ServiceControl Start=both).

Итог: продукт выдерживает отключение сети, kill -9 и stop/start на
обеих платформах. Fail-closed паркинг работает, сеть возвращается.

## Финальная установка из CI-пакетов (2026-10-31)

Оба пакета собраны GitHub Actions (workflow `build-packages.yml`, run 41181923419).
Впервые MSI включает leshy-win, собранный из исходников в CI.

**win10**: MSI установлена поверх живой системы. Core Ready, все 3 роли
desired, awg-main Ready (handshake работает), oc-main Ready, xray-win
Starting→Ready. Трей-UI запущен. Leshy Running.

**ubuntu2604**: deb установлен поверх живой системы. Core Ready, все 3 роли
Ready. Steering работает: `github.com → 140.82.121.3 via kk-awg0 → HTTPS 200`.
`chatgpt.com → 8.47.69.0 via enp0s3` — Leshy ставит /32 маршрут по запросу
DNS (on-demand), при первом обращении из браузера маршрут появится.

**Ответ: ДА — готово к установке на рабочую машину и использованию через UI.**

## Фикс зависания восстановления при протухшей сессии (2026-10-31, commit 4b1b621)

**Находка (продолжение хаос-тестов):** после серии сетевых флапов AWG-туннель
оставался мёртвым до ручного disconnect/connect. Ядро показывало Ready/
Recovering, маршруты стояли, но handshake не проходил — recovery не
запускался вовсе.

**Корневая причина — цепочка из трёх дыр:**

1. `observeToadLocked` не обрабатывал toad-состояние `"reconnecting"`
   (сессия была установлена и протухла при целостном route target).
   Роль оставалась Ready с актуальным ValidatedEpoch → `DecideRole`
   возвращал `ActionNone` → рекавери не стартовало никогда.
2. `DecideRole` при `RoleRecovering` + RouteReady + текущем epoch выбирал
   `ActionValidate` — повторное наблюдение мёртвой сессии без её
   пересоздания. Validate и Rebind не могут восстановить мёртвую сессию,
   только рестарт транспорта (свежий UDP-сокет) или полная замена toad.
3. Мягкая ветка validation-pending ждала вечно; при этом
   `trackRecoveryFailure` засчитывал `ErrValidationPending` в жёсткий
   failure-streak: три быстрых флапа подряд = молчаливое автоотключение
   роли (тот самый «фейл авг» в UI).

**Фиксы:**
- `"reconnecting"` → `RoleRecovering` + сброс ValidatedEpoch (честный UI).
- `DecideRole`: протухшая сессия → `RestartTransport` (TUN сохраняется) или
  `RestartToad` по возможностям бэкенда. Сигнал эмитит только AWG2 —
  OC/xray не затронуты.
- Мягкая ветка validation-pending взводит существующий
  `scheduleRecoveryRetry` (бэкофф 1с→60с) при reconnecting-сессии.
- `ErrValidationPending` не питает failure-streak: роль остаётся
  Recovering с вооружённым ретраем и самолечится, когда DPI отпустит.

**Тесты:** core (`TestRecoveryDecisionRestartsStaleSession...`,
`TestObserveToadReconnectingSessionInvalidatesReadyRole`) + control
(`TestStaleSessionArmsBoundedRecoveryRetryWithoutAutoDisable` — гоняет
повторные ограниченные рестарт-циклы и проверяет отсутствие
автоотключения). Полный `go test ./...` зелёный.

Примечание: на этом хосте наблюдаются редкие сокет-флейки Windows AF_UNIX
("connection refused" при старте тест-сервера) — не связаны с кодом,
лечатся повторным прогоном.

## Установка CI-пакетов 8b468d1 и находка в leshy (2026-10-31, вечер)

**Установка**: MSI (win10) и deb (ubuntu2604) из прогонов Toad core/Build packages
8b468d1 — первый полностью зелёный прогон всех трёх воркфлоу (Toad core,
ShellCheck, Build packages). Для этого починены накопившиеся красные CI-задачи:
darwin-тесты, не компилировавшиеся с самого создания; гонки в тестовых фейках
(fakeLauncher без мьютекса против async SetActiveProfile); незащищённое чтение
пакетной переменной restartHandoffDeadline из утеённых горутин; NM-watcher,
пачкавший продукт-состояние при отменённом контексте; опечатка
"kimora-core.exe" в stage.ps1 при `.\dist-test` (Substring по ненормализованному
пути); устаревший ownership-контракт legacy/external→go/go/go;
shellcheck-находки в 9 скриптах. toadctl.Server.Serve больше не умирает от
разового accept-сбоя (Windows AF_UNIX).

**Стресс на установленных пакетах** (обе ВМ): kill -9 ядра → SCM/systemd
рестарт → все роли Ready, публикации Leshy на месте; stop/start сервиса → то
же; сетевой флап enp0s3 (nmcli) → восстановление. На win10 awg-main при
DPI-флапе корректно ушёл в Recovering ("AWG2 handshake is stale") с
автоматическими ограниченными рестартами транспорта и снятием публикации
(fail-closed) — фикс протухшей сессии работает в бою.

**Находка (главная)**: после рестарта ядра /32-маршруты Leshy на ubuntu не
возвращались — chatgpt.com резолвился Leshy, но шёл мимо туннеля (github.com —
через туннель). Корень: RouteAdder::route_matches на Linux был заглушкой
("доверять книжности", в отличие от Windows). Ядро отзывает маршруты при
fail-closed recovery, in-memory "installed" набор Leshy продолжал считать их
установленными, и каждый последующий DNS-ответ был silent no-op. Фикс:
route_matches для Linux спрашивает ядро, каким маршрутом реально пойдёт адрес,
и сверяет с целью зоны (oif для dev-зон, gateway для via-зон) —
smollgreymouse/leshy ed22d3a. Попутно выяснено: CI собирал leshy из master
(v0.4.0) мимо ветки windows-support со всеми фиксами — master добавлен
fast-forward до windows-support.

Примечание: HTTPS 403 от chatgpt.com через туннель — анти-бот Cloudflare к
датацентровому IP VPS (curl без браузера); напрямую с домашнего провайдера
200. Маршрутизация продукта корректна в обе стороны; браузерный challenge —
вне зоны ответственности ядра.

## Уточнение корня leshy-бага и верификация на VM (2026-10-31, поздний вечер)

Первый вариант route_matches (ядерный фильтр dump по dst/таблице) оказался
недостаточным: RTM_GETROUTE через rtnetlink 0.14 уходит как NLM_F_DUMP, а
ядро (6.x) игнорирует поля фильтра dump-запроса — возвращаются ВСЕ маршруты
всех таблиц, включая таблицу паркинга Kikimora (51890) и connected-маршруты
туннелей. Проверка «нашёлся ли oif целевого устройства» находила чужие
записи (например, connected /32 самого туннеля) и давала ложное
«маршрут на месте».

Финальная реализация (smollgreymouse/leshy d94c7d7): полный dump
фильтруется в userspace — учитываются только записи с точным совпадением
Destination/префикса в таблице main. Проверено сборкой прямо на ubuntu VM:
- рестарт ядра → DNS-запрос chatgpt.com → маршрут автоматически вернулся
  через kk-awg0 (раньше требовался рестарт leshy);
- github.com через туннель: HTTPS 200 (0.7-1.4s) до и после рестарта ядра.

Также выяснено: CI основного репо собирал leshy из default-ветки master
репозитория leshy (v0.4.0), в то время как все фиксы жили на ветке
windows-support — master добавлен fast-forward, пин контракта упаковки
обновлён на 0.5.x. Ручная установка leshy 0.5.1+d94c7d7 на ubuntu
верифицирована; финальные пакеты — CI 41207037166.

## Реальное соединение до ChatGPT и корень «DPI-флапа» AWG2 (2026-10-31, ночь)

**Симптом.** После установки финальных пакетов `awg`/`awg-main` периодически уходил в `Recovering` с причиной `AWG2 handshake is stale`; на Windows роль при этом снимала публикацию зоны `awg-main` (fail-closed), и `chatgpt.com` шёл мимо туннеля. UI в этот момент показывал «connected» — сессия действительно была жива.

**Замер.** Ubuntu, опрос статуса каждые 10 с: возраст handshake 10 → 20 → 30 с, роль → `Recovering`, recovery перезапускает транспорт, новый handshake (≈9 с). Цикл ≈ 60–90 с. То есть ядро само рвало живой туннель.

**Корень.** `toad/internal/backend/awg2/health.go`: `recentHandshakeWindow = 30s`. WireGuard перезаключает handshake по `REKEY_AFTER_TIME` = 120 с, а сессию старше `REJECT_AFTER_TIME` = 180 с использовать запрещено. Persistent keepalive — это transport-пакеты, они handshake не обновляют. Здоровая idle-сессия законно имеет возраст handshake до ~120 с, поэтому порог 30 с гарантированно объявлял её протухшей раз в цикл рекея. На Windows это дополнительно снимало публикацию зоны.

**Фикс** (коммит 4ccdda3): окно = 180 с (`REJECT_AFTER_TIME`), `online` только при возрасте строго меньше 180 с. Тест `TestParseHealthUAPIToleratesProtocolRekeyAge` (119/120/179 с — online), stale-тест переведён на 181 с. `go test ./...` зелёный.

**Проверка на ВМ после фикса.**
- Ubuntu: 3+ минуты наблюдения — handshake вырос 16 → 117 с, прошёл рекей, роль всё время `Ready`, публикация не снималась.
- Windows: после замены бинарников `oc-main` и `xray-win` сразу `Ready` (застрявший `source-address-changed` снялся перезапуском сервиса), `awg-main` подключился за 5 с, зона `awg-main.dev` опубликована.

**Реальное соединение до ChatGPT.**
- Ubuntu: `resolvectl` → 8.6.112.0/8.47.69.0; `ip route get` → `dev kk-awg0 src 10.8.1.3`; `https://chatgpt.com/cdn-cgi/trace` → `ip=72.56.119.116`, `colo=AMS`, `loc=DE` (адрес VPS); `GET /` с браузерным профилем (TLS 1.2, HTTP/1.1, Chrome UA) → **200**; тот же запрос голым curl → 403 `cf-mitigated: challenge` (фингерпринт curl, не маршрут); `api.openai.com/v1/models` → 401 без ключа (API доступен через туннель).
- Windows: NRPT-правила `.chatgpt.com`, `.openai.com`, `.oaistatic.com`, `.oaiusercontent.com` → 127.0.0.1; Leshy видел запрос `chatgpt.com`; `Find-NetRoute` → `kk-awg0`; trace → `ip=72.56.119.116`; `GET /` → **200**; счётчики `kk-awg0` выросли (rx 0 → 684 812, tx 4 578 → 28 105).

**Лабораторное ограничение.** На обеих ВМ одинаковые AWG `private_key`, `peer_public_key` и адрес `10.8.1.3/32` — это один peer на сервере, клиенты вытесняют друг друга на каждом handshake. Поэтому Windows проверялся при отключённом `awg` на Ubuntu. Для одновременной работы нужен второй peer (свой ключ и, например, `10.8.1.4/32`).

**Осталось разобрать.** До перезапуска `oc-main` и `xray-win` висели в `Recovering` с причиной `source-address-changed` (`validated_underlay_epoch=0`, `recovery={}`) при живом toad (`online`) и здоровой валидации; перезапуск сервиса это снимает. Похоже, после смены preferred source роль может остаться в `Recovering` без запланированного recovery/валидации — нужен отдельный тест.

**Бинарники на ВМ.** Локальная сборка из 4ccdda3 (`CGO_ENABLED=0`, linux/amd64 и windows/amd64): toad sha256 `60eeb0bb…` (Linux) / `0034946c…` (Windows), core `B20E74B9…`. Бэкапы: `/usr/local/bin/kikimora-toad.bak-0.5.1` и `C:\ProgramData\Kikimora\kikimora-toad.exe.bak-0.5.1`. Версия локальной сборки core на Windows — `v0.1.0-dev` (без ldflags CI), на Ubuntu — `1.0.0`.

## ChatGPT в браузере на Windows: AAAA уводил трафик мимо туннеля (2026-10-31, ночь)

**Симптом.** На Ubuntu ChatGPT открывался в браузере, на Windows — нет («Unable to load site»), хотя `curl` через туннель получал 200.

**Что нашли.**
1. Сначала `awg-main` на Windows был вообще остановлен (я снял его, чтобы две ВМ не дрались за один peer) — браузер шёл напрямую.
2. После включения роли страница всё равно падала: Edge получал AAAA (chatgpt.com — Cloudflare с IPv6), Leshy ставил /128-маршрут в `kk-awg0`, но у интерфейса нет глобального IPv6 — пакет уходил с source = IPv6 андерлея, Cloudflare показывал в ошибке домашний IPv6 гостя, а не адрес VPS. `curl -6` в тех же условиях — таймаут/403.
3. На Ubuntu тот же домен работал, потому что браузер выбирал IPv4.

**Фикс (Leshy 0.5.2, smollgreymouse/leshy 277fd6d).** Для зон с `route_type = "dev"` Leshy больше не отдаёт AAAA и не ставит IPv6-маршруты, пока в зоне явно не указано `ipv6 = true`. Клиент получает только A → маршрут в туннель. `via`-зоны не тронуты.

**Проверка.**
- `cargo test`: 59 + 6 + 4 + 5 тестов зелёные (включая новые: фильтр AAAA и политику зон).
- Ubuntu: `dig A` → 2 записи, `dig AAAA` → NODATA; leshy 0.5.2.
- Windows: `Resolve-DnsName AAAA` → 0 записей после `ipconfig /flushdns`; страница ChatGPT в Edge открывается («С чего начнем?», поле ввода) — и до, и после апгрейда Leshy до 0.5.2.
- В UI Kikimora — «Protected», `awg-main` Ready; Edge грузит страницу через `kk-awg0`.

**Состояние лаборатории.** Windows держит `awg-main`, Ubuntu `awg` отпущен, чтобы не воевать за общий peer. Для одновременной работы нужен второй peer на сервере.

**Установлено.** Leshy 0.5.2: Linux `/usr/local/bin/leshy` (бэкап `leshy.bak-0.5.1`), Windows `C:\Program Files\Kikimora\leshy\leshy.exe` (бэкап `leshy.exe.bak-0.5.1`).
