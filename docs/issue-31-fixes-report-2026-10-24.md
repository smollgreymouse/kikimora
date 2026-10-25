# Отчёт по фиксам issue #31 — TCP DNS + чистый teardown DisconnectAll (2026-10-24)

Основание: [issue #31](https://github.com/smollgreymouse/kikimora/issues/31). Ветка: `feat/amnezia-inspired-ui` (содержит целевую `feat/native-core-vpn-clients`). Все изменения **незакоммичены**, готовы к ревью.

## Сделано

### 1. DNS-транспорт (`dns_protocol`) в генераторе Leshy-конфига
- `toad/internal/config/leshy_config.go`: у `LeshyServer` новое поле `DNSProtocol`; `ReadLeshyServer` читает `[server] dns_protocol`; `WriteLeshyConfig` пишет ключ только при заданном значении; `ValidateDNSProtocol` — только `""/udp/tcp`, **`auto` отвергается намеренно** (в Leshy нет fallback, обещать нельзя).
- `toad/cmd/kikimora-core/generate.go`: флаг `--dns-protocol` с ранней валидацией; пустой = сохранить из существующего конфига.
- Эффект: полевой фикс (`dns_protocol = "tcp"` на сети с заблокированным UDP/53) переживает регенерацию конфига ядром. Явный `"udp"` тоже сохраняется.
- Тесты: `TestDNSProtocolPreservedThroughRegeneration`, `TestDNSProtocolAbsentMeansNoKey`, `TestWriteLeshyConfigRejectsUnknownDNSProtocol`, `TestReadLeshyServerDefaultsOnBrokenFile`, `TestDNSProtocolExplicitUDPPreserved`.
- Ревью: OK (исправлены gofmt, ранняя валидация флага, добавлен тест на явный udp).

### 2. Снятие сентинелов таблицы 51890
- `toad/internal/platform/linux/netlink/routes.go`, `endpointPolicyPlan(remove=true)`: когда в таблице не осталось правил чужого priority (владелец = `rule_priority`, дубликаты запрещены `ValidateEndpointPolicies`), план удаляет `unreachable` 0.0.0.0/0 и `::/0` — осиротевшая таблица-блэкхол больше не остаётся.
- Безопасность: фильтр по `endpointRouteProtocol`; только последний владелец; повторный remove — no-op.
- Тесты: `TestRemovePlanDeletesSentinelsWhenLastOwnerLeaves`, `TestRemovePlanKeepsSentinelsWhileOtherOwnerRemains`, `TestRemovePlanWithoutSentinelsIsQuiet`, `TestRemovePlanIsIdempotent`.
- Ревью: OK.

### 3. Teardown ресурсов в DisconnectRole/DisconnectAll
- `toad/internal/core/engine.go`: в контракт `RecoveryDriver` добавлен `ReleaseResources(ctx, role)` — «неполный релиз = ошибка».
- `toad/internal/control/recovery_driver.go`: имплементация — withdraw Leshy-публикации (при наличии zone), `RemoveEndpointPolicy` (правила + host-маршруты + сентинелы для последнего владельца), `parking.Manager.Clear` (снятие fail-closed парковок), очистка ownership-чекпоинта (в памяти и на диске); при ошибке — joined error + инвентарь остатков (`parks-active`, `owned-routes`, `publication`).
- `toad/internal/control/control.go`: `DisconnectRole`/`DisconnectAll` после остановки процесса Toad вызывают `releaseRoleResources`; неполный cleanup ставит роли `lastError = "cleanup incomplete: ..."` и возвращает ошибку — чистый успех никогда не фальсифицируется. В `DisconnectAll` ошибка одной роли не останавливает освобождение остальных.
- Тесты: `toad/internal/control/disconnect_teardown_test.go` — полный релиз (верифицированные через чекпоинт парковки на фейковом ядре, публикация, чекпоинт обнулён, ровно один `RemoveEndpointPolicy`), cleanup-провал → ошибка + остаток на роли, DisconnectAll на двух ролях.
- **Ревью субагентом на полной GLM-5.3** (workflow с тест-гейтом): вердикт **ПРОБЛЕМЫ** — 1 high, 3 medium, 4 low. Все блокирующие и обоснованные исправлены:
  - **[high] Ложный успех при унаследованных от краха парковках**: `ReleaseResources` чистил парковки только по in-memory состоянию менеджера; парки крашнувшегося процесса живут в ядре и в чекпоинте на диске. Исправлено: `restoreOwnershipCheckpoint` вызывается перед `Clear` (как в `Park`); тест `TestDisconnectRoleReleasesCrashInheritedParks` (парковка в ядре+чекпоинте при пустом in-memory менеджере → после дисконнекта в ядре парковок нет).
  - **[medium] Инвентарь остатков всегда показывал нули** (считался после мутаций): инвентарь теперь фиксируется до мутаций (`before release: parks-active, parked-prefixes, owned-routes, publication`).
  - **[medium] Изоляция ролей держалась на `ValidateEndpointPolicies`, который в проде не вызывался**: валидация (уникальность `rule_priority` + теперь и уникальность `leshy_zone`) вызывается в `newManagerWithDeps`; конфликтующий набор конфигов отклоняется на загрузке. Тест `TestDisconnectRoleLeavesSiblingResourcesAlone` (дисконнект одной роли не трогает политику и публикацию сиблинга).
  - **[medium] `ReleaseResources` без дедлайна**: `releaseRoleResources` оборачивает вызов в `context.WithTimeout(30s)`, `ReleaseProduct` прокидывает свой ctx.
  - **[low] Ошибка `stopRoleProcess` пропускала release** (ресурсы молча переживали дисконнект): теперь release выполняется всегда, ошибки джойнятся (`DisconnectRole`, `DisconnectAll`, `ReleaseProduct`).
  - **[low] Гонка с конкурентным `ConnectRole`**: явный дисконнект перед release проверяет, что роль не включена заново; graceful-stop (`ReleaseProduct`) передаёт `skipIfEnabled=false`, т.к. там релизятся именно ещё желаемые роли.
  - **[low] Политика с priority, но без зоны** получала ошибку «invalid endpoint policy»: удаление вызывается только при `policy.Valid()` — без зоны применять было нечего.
  - Отложено осознанно: из remaining-замечаний ревью — расширение тест-покрытия DisconnectAll посевом всех ресурсов и assert на текст инвентаря.

### 4. Чистый релиз при graceful shutdown (этап 4)
- `toad/internal/control/control.go`: новый `Manager.ReleaseProduct(ctx)` — graceful-stop teardown: та же чистая сеть, что после `DisconnectAll` (публикации, endpoint-политики, сентинелы, парковки, чекпоинты сняты), процессы остановлены, **персистентный интент оператора сохранён** — следующий старт переподключает желаемые роли. `ShutdownProduct` остаётся fail-closed (транзиентный рестарт).
- Различение stop/restart: маркер рестарта. `control.RestartMarkerPath()` (`/run/kikimora/core-restart.pending`, оверрайд `KIKIMORA_RESTART_MARKER`): маркер существует на момент SIGTERM → стоп транзиентный, парки выживают; обычный стоп → `ReleaseProduct`. `toad/cmd/kikimora-core/main.go` (`finalizeShutdown`) вызывает `ReleaseProduct` после остановки control-цикла, если маркера нет.
- Юниты: `kikimora-core.service` и `kikimora-core-next.service` получили `ExecStopPost=-/bin/rm -f /run/kikimora/core-restart.pending` — сбитый рестарт не может превратить последующий обычный стоп в fail-closed.
- Оркестрация: `packaging/linux/stage-release.sh` (postinst и postrm failed-upgrade) ставит маркер перед `systemctl restart kikimora-core.service`; `linux/tests/toad/vm-dualstack-regression.sh` ставит маркер перед рестартом в тесте восстановления (там проверяется именно транзиентный рестарт, а не чистый релиз).
- Тесты: `TestReleaseProductMatchesDisconnectAllState` (сеть чистая, интент сохранён, `Close()` после релиза — no-op), `TestRestartMarkerPathEnvOverride`.

## Приёмочные гейты на живой Ubuntu VM (2026-10-25)

`linux/tests/toad/vm-disconnect-teardown-acceptance.sh` на лабовой VM (три роли awg/oc/xray-nl, новое ядро) — **все выполнимые гейты прошли**:

- DisconnectRole: освобождает только свою роль (публикация ушла, сиблинг остался Ready), реконнект работает;
- DisconnectAll: ноль принадлежащих маршрутов/правил/парковок/публикаций, повторный вызов не мутирует сеть (идемпотентность), ядро живо и принимает ConnectAll;
- graceful stop (`systemctl stop`, без маркера): та же чистая сеть, что после DisconnectAll; DNS-владелец остановился вместе с ядром (PartOf);
- рестарт с маркером: роли восстановились, маркер снят ExecStopPost'ом;
- **fixture полевого инцидента**: с блокированным UDP/53 и `dns_protocol = "tcp"` Leshy резолвит; после восстановления конфига — резолвит снова;
- SKIP (честно зафиксирован): передача сети внешнему системному VPN — нужен второй VPN-клиент на VM.

Два продукта-находки прогона, исправленные там же: валидация дубликатов `rule_priority` переведена на предупреждение (легаси-конфиг лабы с общим приоритетом не должен окидывать ядро), `xray-nl` на лабе получил отдельный приоритет 53.

## Тесты на обеих лабовых VM (2026-10-25)

**Ubuntu VM (192.168.1.51, Qt 6.10.2)**: `go test ./...` по toad — все пакеты ok; desktop `ctest` — **9/9** (включая новые ui-behavior кейсы: диагностика/наблюдатели, bypass-карточка, полный диагностический drawer).

**Windows VM (192.168.1.246, Qt 6.10.2 mingw)**: `go test ./...` — все пакеты ok (control, config, windows/routes…); desktop `ctest` — **8/9**. Историческое событие: **`real-core-e2e` прошёл впервые** (клиент научился AF_UNIX-транспорту Windows — ишью из аудита 10-17 закрыто).

**`real-core-client` (4 кейса: клиент не применяет ни один снапшот, revision=0) — НАЙДЕН И ПОЧИНЕН.** Корень: фикстура теста слушала через `QLocalServer` (именованный пайп Windows), а переписанный клиент 21–23 октября ходит через `CoreSocket`, который на Windows говорит **AF_UNIX поверх Winsock напрямую** — разные семейства сокетов, соединение невозможно в принципе (на Linux CoreSocket оборачивает тот же QLocalSocket, поэтому там тест зелёный). Это премжизнь ветки, не связанная с правками 25 октября. Фикс: в `desktop/tests/RealCoreClientTest.cpp` фикстура на Windows теперь слушает AF_UNIX (зеркало CoreSocket: bind/listen/accept через QSocketNotifier), QLocalServer остался на Unix. Результат на Windows: **9/9 ctest, все go-пакеты ok** — впервые на этой ветке. `real-core-e2e` подтверждён по-настоящему (3 passed против живого ядра, 3121ms, после сборки `kikimora-core-local.exe`/`fake-toad.exe` от HEAD).

## Не сделано (follow-up)
1. **Передача сети внешнему системному VPN** (гейт 6 ишью) — требует второго VPN-клиента на VM.
2. **Смена Wi-Fi-шлюза/DNS во время работы** (гейт 9 в части NetworkManager-динамики) — fixture UDP/TCP покрыт, живая смена сетей не прогонялась.
3. **Инъекция сбоев cleanup** (гейт 10: `kk doctor` показывает остатки — реализовано; искусственные отказы netlink/DNS не инъектировались).
4. **DNS-relinquish при `DisconnectAll`**: сейчас DNS-владелец уходит при остановке ядра (PartOf); при живом ядре с отключёнными ролями интеграция остаётся активной.
5. **Установка на Windows обновлена**: `kikimora-core-local.exe`/`fake-toad.exe` пересобраны от HEAD; обновление установленной в систему копии (трей) выполнено отдельным агентом.

## Верификация
- В `toad/`: `go build ./...`, `go vet ./...`, `go test ./...` — 23 пакета ok, отказов нет.
- `gofmt -l` — чисто.
