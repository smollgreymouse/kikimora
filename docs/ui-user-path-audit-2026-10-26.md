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
