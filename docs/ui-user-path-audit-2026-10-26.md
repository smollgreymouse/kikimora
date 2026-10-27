# Аудит пользовательских путей UI ↔ ядро (2026-10-26)

Прогон по каждому контролу UI (две страницы + drawer'ы + трей) с трассировкой
пути пользователя до ядра и обратно. Исходная точка: ветка
`feat/amnezia-inspired-ui` @ `e7ee1d2` (после стабилизации оборванного WIP:
дубль `onRoleDataChanged` в `KRoleDetailDrawer.qml` ронял компиляцию QML,
тест membership был переписан под несуществующий `roleProfileCombo`, а
семантика ядра (`SetProfileRoles`, снапшот `roles[].profiles`) и фейка —
чекбокс-мультимножество).

## Карта контролов и путей

| Контрол | Путь пользователя | Метод ядра | Покрытие |
|---|---|---|---|
| Круг connect | клик → toggleAll → ConnectAll/DisconnectAll | `ConnectAll`/`DisconnectAll` | `connectCircleDrivesGlobalLifecycle`, `connectCircleStaysActionableAndCalmInPartialState` |
| Свитчер профиля | выбор профиля → роли фильтруются, чужие останавливаются | `SetActiveProfile` | `homeProfileSelectionAndCreate`, Go `TestSetActiveProfileSwitchesRoles` |
| «+» создать профиль | модал → имя+роли → профиль появляется сразу, без переключения | `CreateProfile` | `createProfileFlowValidatesAndCreates` |
| Строка роли | клик → drawer: состояние, действия | снапшот + `RetryRole`/`DisconnectRole` | `drawerOpensAndControlsOneRole`, `drawerActionsAreCoreReported` |
| Membership роли | чекбоксы профилей → Save | `SetTunnelProfiles` | `roleDrawerEditsMembershipAndRoutes` (фейк) |
| Маршруты роли | редактор → Save → Leshy перечитывает | `SetRoutingList` (+regen+reload) | `roleDrawerEditsMembershipAndRoutes` |
| Удалить роль | диалог подтверждения → конфиг удаляется | `RemoveTunnel` | — |
| Добавить туннель | профиль-дровер → vless/https/JSON → import | `CreateTunnel` | `createFlowsAddProfileAndTunnel` (фейк) |
| Default-route (bypass) | выбор «напрямую/роль» | `SetBypassMode` (+regen+reload) | `defaultRouteSelectionDrivesCore` |
| Диагностика | observers, версия ядра, полная диагностика | `GetDiagnosticsSnapshot` | `settingsDiagnosticsShowsCoreInfoAndObservers` |
| Трей | закрыть в трей, меню connect/disconnect | те же команды | `tray-live`, `tray-menu` |

## Находки (дырки пути пользователя)

### F1 (high): сохранённое membership роли откатывается UI назад
Путь: drawer роли → чекбоксы профилей → «Сохранить профили» → `SetTunnelProfiles`.
Ядро (`control.SetTunnelProfiles`, config_api.go:30) пишет только диск: в памяти
`r.cfg.Profiles` и снапшот `roles[].profiles` остаются старыми, revision не
двигается, ответ прямо говорит «restart the core to load it». Клиент пишет
membership оптимистично (`RealCoreClient::setTunnelProfiles` → `m_roleProfiles`),
но следующий снапшот затирает его старым значением (`RealCoreClient.cpp:579`) —
пользователь видит, как его сохранение «отменяется».
**Фикс:** ядро обновляет membership сразу (диск → память → rebuildProfileMap →
bump), как уже делает `SetProfileRoles`; сообщение без «restart».

### F2 (high): переключение профиля бросает ресурсы выведенных ролей
Путь: свитчер профиля → роли вне нового профиля останавливаются
(`SetActiveProfile` → `stopRoleProcess`), но в отличие от `DisconnectRole`/
`DisconnectAll`/`ReleaseProduct` **release не вызывается**: Leshy-публикации,
endpoint-политики (правила + host-маршруты + сентинелы таблицы 51890),
fail-closed парковки и ownership-чекпоинт остаются указывать в мёртвый туннель.
**Фикс:** stop + release с joined-ошибкой (ошибка релиза не откатывает
переключение, но остаётся на роли), тест на чистую сеть после свитча.

### F3 (medium): Retry при живом процессе — тихий no-op
Путь: роль Recovering → drawer → «retry» → `RetryRole` → ядро молча возвращает
ok (`if r.process != nil … return nil`). Именно это было на win10 2026-10-25
21:40:22 (пользователь жал retry при замороженном recovery — ноль эффекта).
**Фикс:** `RetryRole` при живом процессе в Recovering взводит немедленный
recovery-ретрай (без бэкоффа); no-op остаётся только для Ready.

### F4 (medium): после создания/удаления туннеля UI не объясняет, что нужен рестарт ядра
Ядро отвечает «created …; restart the core to load it», но `Core.lastCommandMessage`
в QML рендерится только в статус-строке маршрутов (`KRoleDetailDrawer.qml:397`) —
в профиль-дровере пользователь добавил туннель и не видит ни роли, ни причины.
Фейк-бэкенд добавляет роль сразу (для детерминизма тестов), что маскирует разрыв.
**Фикс:** профиль-дровер показывает ответ ядра рядом с кнопкой «Добавить».

### F5 (info, осознанно не фиксим сейчас): Create/RemoveTunnel не живут до рестарта
Композиция фиксирована при старте менеджера. Живая загрузка нового туннеля —
отдельная фича (инвентарь швов: launcher/roles/profileToRoles/персист). Пока
честная мессага (F4) + follow-up в роадмап.

## Добор покрытия по сбоям и сетевым событиям (запрос 2026-10-26)

- Wi-Fi/сеть налету: семантика `netstate.Compare` — смена интерфейса/шлюза/
  preferred-source/BSSID даёт материальный epoch+1 (тесты в `netstate`);
  material epoch на живой роли → пере-валидация на новом epoch без рестарта
  транспорта (тест в `control`).
- Сбои: endpoint resolution pending на стабильной сети не замораживает роль
  навсегда (регресс `TestEndpointPendingRecoveryHandsBackOnLaterReadySnapshot`),
  отмена recovery дисконнектом (`TestDisconnectCancelsInFlightRecovery`).
- DNS: `SetRoutingList` регенерирует конфиг и перезапускает Leshy (api.go) —
  путь проверен существующими netns-скриптами; детерминизм materiality для
  DNS-метаданных — в `netstate`.
- IPv6: выпадение/возврат одной семьи — материальный `ChangeAvailability`
  (`netstate`), роль с IPv4-endpoint под IPv6-only underlay уходит в
  Recovering, не в Failed (существующие `TestEngineKeeps*`).
- NetworkManager/виндоус-аналоги: наблюдатели снапшотятся в
  `observers.networkmanager_healthy` (UI), нативные нотификации winipcfg на
  Windows — приёмка на VM (см. roadmap 08a4), модельные тесты — в `netstate`.

## Что уже было зелёным на старте аудита

19 UI-сценариев (`ctest` 9/9), 26 Go-пакетов toad, включая регрессии
recovery-заморозки и teardown-дисконнекта из 2026-10-25/26.

## Реализация (2026-10-26/27)

| Находка | Фикс | Регресс |
|---|---|---|
| F1 membership откатывался | `bf89146` (+ уточнение `ddd52fb`: disk-only ход для ещё не загруженных ролей, пустой набор отклоняется) | `TestSetTunnelProfilesAppliesWithoutRestart` |
| F2 свитч бросал ресурсы | `f076ca0` — stop+release для выведенных ролей | `TestSetActiveProfileReleasesDepartingRoles` (красный без фикса) |
| F3 retry — тихий no-op | `b453c4d` — кик recovery-транзакции для Recovering с живым процессом, no-op только для Ready | `TestRetryRoleKicksOrphanedRecoveringTransaction` (красный без фикса), `TestRetryRoleReadyIsQuietNoOp` |
| F4 мессага о рестарте не видна | `6d38586` — `tunnelImportStatus` в профиль-дровере | `createFlowsAddProfileAndTunnel` |
| WIP-обрыв (дубль handler, combo-тест) | `e7ee1d2` | существующие 19 UI-сценариев |
| добор: wifi→ethernet switch | `ddd52fb` | `TestCompareDetectsWiFiToEthernetSwitch` |
| добор: revalidation на новом epoch | `ddd52fb` | `TestMaterialUnderlayChangeRevalidatesReadyRoleOnNewEpoch` |

DNS/маршруты на живом ядре: `SetRoutingList` → регенерация конфига → рестарт
Leshy уже покрыт (`TestHandleRoutingLists`, `reloads==1`); IPv6-выпадение —
`TestCompareDetectsFamilyAvailabilityChange` + существующие
`TestEngineKeepsMissingEndpointFamilyRecovering`; сбои — регрессы
endpoint-pending handoff и отмены recovery от 2026-10-26.

## Живой гейт на ubuntu2604 (2026-10-27, ядро от ddd52fb)

Ядро пересобрано под linux, развёрнуто вместо установленного
(`/usr/local/bin/kikimora-core`, сервис `kikimora-core.service`), гейт
`tmp/profile-gate.py` (не коммитится) гоняет реальный сокет
`/run/kikimora/core.sock` — **GATE PASS**:

- baseline: default-профиль, awg/oc/xray-nl — Ready, ip-правил 51890 — 3,
  публикации `awg.dev oc.dev xray-nl.dev`;
- `CreateProfile(audit-gate, [awg])` + `SetActiveProfile(audit-gate)`:
  oc и xray-nl остановились **и освободили ресурсы** — публикации остались
  только `awg.dev`, правила 51890: 3→1 (F2 без фиксы оставил бы чужие);
- `SetTunnelProfiles(awg, [default, audit-gate])` — membership виден в живом
  снапшоте без рестарта (F1);
- восстановление: default-профиль, все роли desired, скретч-профиль вычищен
  из `profiles.list` и `awg.toml`, после рестарта — только `default`, все
  три роли Ready.

После гейта на ВМ остаётся фиксовое ядро от `ddd52fb` (позже заменится
очередным deb-релизом).

## Инфра-заметка

SFTP на лабовом канале сломан провайдерским DPI (paramiko `sftp.put` —
ENOENT/обрывы на любом размере; крупные exec-стримы тоже рвутся).
Рабочие каналы: `scp` из Git Bash (бинарник 8 МБ прошёл, чексуммы совпали)
и мелкие exec-порции; `tmp/vm_put.py`/`win_put.py` на этом канале
ненадёжны.

## Деплой на лабовые ВМ (2026-10-27, вечер)

- **Ядро** `3392d55` стоит на обеих ВМ: ubuntu
  (`/usr/local/bin/kikimora-core`, systemd-сервис) и win10
  (`C:\Program Files\Kikimora\kikimora-core.exe`, сервис KikimoraCore);
  на обеих все роли Ready.
- **UI**: на ubuntu старый deb-бинарник заменён свежей сборкой от `2225212`
  (собрана прямо на ВМ, Qt 6.10.2, `/usr/bin/kikimora-ui`), процесс
  перезапущен в Wayland-сессии. Попутно найдена и закрыта несовместимость
  с Qt 6.10: `KDefaultRouteRow` делегировал ре-резолв индекса через
  `Qt.callLater`, который продолжал стрелять по уничтоженному роли-ряду —
  на 6.10 это валило «invalid context» TypeError при каждой пересборке
  списка (фикс `2225212`: отложенный ре-резолв на дочернем Timer'е).
  `ctest` на ВМ: 9/9, ноль indexForMode-предупреждений. UI win10 ВМ
  обновляется очередной пересборкой MSI.
- **Флап awg на убунте 17:26:35**: три подряд неуспешные валидации
  «awaiting AWG2 handshake» → роль по дизайну осела в Failed; одновременно
  OpenConnect рапортовал «Set up UDP failed; using SSL instead» — DPI
  провайдера душил UDP (AWG-handshake и DTLS оба UDP). Восстановился
  рестартом ядра при деплое (handshake прошёл); Failed сам не ретраится —
  ждёт ручного retry по дизайну.

## UI на win10 ВМ тоже свежий (2026-10-27, поздний вечер)

Правило оператора: после правок UI/ядра обновлять **обе** ВМ, не только
ubuntu. UI win10 собран прямо на ВМ (msys64 mingw + `C:\Qt\6.10.2\mingw_64`,
Release, от `2225212`), заменён `C:\Program Files\Kikimora\ui\kikimora-ui.exe`
(старый сохранён как `.bak-20261027`), процесс перезапущен в консольной
сессии. Грабли рестарта из SSH: `schtasks /TR` съедает кавычки пути
(Last Result 0x80070002), `Start-Process` из SSH даёт невидимую сессию 0;
рабочий способ — `Register-ScheduledTask` с
`-LogonType Interactive` (процесс попадает в Session 1/Console).
Итог: обе ВМ — ядро `3392d55` + UI `2225212`.

## Проверка сборки установочных пакетов (2026-10-27, ночь)

Обе упаковочные линии чинились от чистого чекаута и прогнаны до зелёного.

**Windows (stage.ps1 → MSI + portable zip, `test_package.ps1` 44/44):**
четыре накопившихся полома: `light` линковал без WixUIExtension
(LGHT0094 на `WixUI_FeatureTree` из c2122f6); кириллица финиш-диалога не
влезала в кодовую страницу 1252 (LGHT0311 — Product теперь ru-ru/1251 +
`-loc ru-ru.wxl`, визард заодно стал русским); heat-фрагмент
`ui-payload.wxs` (Qt-рантайм UI) вообще не компилировался stage.ps1 —
компонент UiExe выпадал из Feature (ICE21) и трей не установился бы;
входные пути резолвились после смены cwd скрипта, молча теряя
`-dUiPresented`. Плюс: wintun-дропин снабжён sha256-пином, пин гейтит
только дропин, не явный `-WintunDll`. Артефакты: MSI 69МБ (ядро+toad+UI с
полным Qt-рантаймом), zip 69МБ.

**Linux (build-release.sh → deb + tar.gz, `test_release_packaging.sh`
PASS):** рефактор aac0bdf удалил `linux/files/build-config-go` и статические
seeds `domains/primary.txt`+`secondary.txt`, но stage-release.sh продолжал
их ставить — свежий чекаут не собирал deb вовсе. `build-config-go` теперь
стейджится как ширма над `kikimora-core generate-leshy` (та же, что
build-config), мёртвые seeds убраны, тест ожидает их отсутствие. Полный
пакет собран на ubuntu2604 c `KIKIMORA_LESHY_SRC` (форк-leshy, собранный
cargo) и `KIKIMORA_UI_BIN` (свежий UI 2225212): deb содержит ядро, toad,
kk-CLI, leshy, UI с desktop-entry и все шимы.

Гейты: `stage-exit=0`, `test-exit=0` (44 ok), `PKGTEST_EXIT=0`
(chapter-08 console package PASS, чексумма deb зафиксирована в логе).

## Отчёт оператора с win10: свитч профиля «не сработал с первого раза» (2026-10-27, ночь)

Симптом: создал профиль 42, выбрал его — список ролей остался от default,
пришлось выбирать в меню повторно; кнопка «подтупливает».

**Корень найден в core-debug.log вин-ВМ:**
`SetActiveProfile profile="42" took=5.596s` — первый свитч пять с половиной
секунд обрабатывался ядром СИНХРОННО в обработчике API (остановка toad'ов:
control-socket stop + teardown + release + запуск новых процессов). Всё это
время клиент не имел ответа: свитчер и список ролей висели на default.
Второй вызов через 3с — `took=0s` (уже активен, no-op). «Подтупливание
кнопки» — тот же stall.

**Фиксы (`9589abd` + `b076720`, на обеих ВМ):**
- Ядро: desired-state, персист и bump коммитятся сразу — API отвечает
  мгновенно; остановка/запуск/релиз ролей ушли в фоновую горутину и
  доезжают через обычный поток снапшотов (Starting/Stopping видны честно).
- Клиент: оптимистичное отображение выбранного профиля; ответ ядра
  подтверждает, отказ откатывает показанное значение на прежнее.
- Фейк-бэкенд выровнен с реальным контрактом снапшота: default всегда в
  списке профилей и стартовый активный профиль — default (раньше фейк
  стартовал на home и вообще не содержал default в списке — свитчер не мог
  вернуться на default в принципе).
- Тесты теперь гоняют продовый Basic-стиль (main.cpp его пинует, тесты —
  раньше нет: комбобоксы вели себя иначе). Новый регресс
  `profileSwitchThroughThePopup`: создание профиля → выбор в попапе реальными
  кликами → список ролей обновляется с первого выбора. ui-behavior: 22/22.

Деплой: ядро+UI обновлены на win10 (KikimoraCore перезапущен, все роли
Ready, UI в Console-сессии) и ubuntu2604 (сервис active, UI перезапущен).
