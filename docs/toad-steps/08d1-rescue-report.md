# Отчёт о rescue R1–R11 (08D1, 2026-11-09)

Статус: **RESCUE COMPLETE**. Выполнен обязательный пакет
`docs/toad-steps/08d1-post-l11-rescue-plan.md`, этапы R1–R11.
L15 разблокирован; K17, v0.6.0 и релизные работы остаются под
section-35 release stop rule.

Итоговые головы:

~~~text
Leshy:    0da1b9ffe51f891b41b0c2bc843b6d9250824a49 (master)
Kikimora: 762a91e736f78e2fd45ab602a56411c6dc8bfb8b (master, до этого отчёта)
Компаньон: LESHY_REF=0da1b9ffe51f891b41b0c2bc843b6d9250824a49
~~~

## 1. Таблица этапов

| Этап | Репозиторий | Коммит | GitHub run | Результат |
|---|---|---|---|---|
| R1 | leshy | `05b47699bda6d3e5a68f62f883d22e88f39778ba` | 42670483067 | 7 обычных jobs success; privileged netns failure (ожидаемо до R2) |
| R2 | leshy | `432e33c2af716817634a7bdaa8ce5c0572cb0625` | 42672226556 | Privileged netns success (job 129348816325) |
| R3 | leshy | `a66550528dfea48fe1c0faa8ef2f143c7f88e696` → `bcb9665709f39ac6c195f6d59f2ed7241a9357da` | 42673966379 attempt 2 | 8/8 success (job 129352953173) |
| R4 | kikimora | `da8e72ce73a1ec5a242d72652358a0db217148c8` | — | запись приёмки L11 в execution log |
| R5 | leshy | `fcf723f57071f2150284e4db1693a8b4225e66eb`, `70c9839326fdd05f0f2fba90478d87d936f37787` | 42676819410 | 8/8 success + native Windows gate 9/9 |
| R6 | leshy | `0da1b9ffe51f891b41b0c2bc843b6d9250824a49` | 42677847701 | 8/8 success, L13 переподтверждён |
| R7 | leshy | `0da1b9ffe51f891b41b0c2bc843b6d9250824a49` | 42677847701 | 8/8 success, L14 принят |
| R8 | kikimora | `fe06b13bbedfefad317d5c240fb2956d8bfc7ad7` | — | восстановлена история журнала |
| R9 | kikimora | `3b8859ceb279c033e168fb479fb6a1ae1b85ae1f` | — | pin 40-hex + валидация |
| R10 | kikimora | pin-only | 42678886758 | Resolve pin / Linux deb / Windows MSI — 3/3 success |
| R11 | kikimora | `762a91e736f78e2fd45ab602a56411c6dc8bfb8b` | — | §55 parent packet: L11–L14 ACCEPTED |

## 2. Что изменено по этапам

### R1 — собираемость и реальные L14 reload-тесты

Коммит `05b4769` `fix(routing): restore L14 reload tests and buildability`.

- Восстановлены как настоящие `#[tokio::test]` и переведены на
  `crate::server::apply_reload`:
  `reload_rejects_kikimora_socket_changes`,
  `reload_rejects_capacity_changes`,
  `reload_rejects_aggregation_prefix_changes`,
  `reload_two_zones_only_changed_one_is_targeted`.
- Добавлен `reload_rejects_routing_backend_changes` — четвёртое
  restart-only поле.
- Каждый restart-only тест проверяет текст `restart Leshy`, неизменность
  живого конфига и нулевые ensure/remove/snapshot на fake-backend.
- Двухзонный тест: ретирится только изменённая зона, неизменная не
  трогается.
- Удалён только случайный дубль
  `managed_reload_sends_zero_controller_calls_until_changed`; отдельное
  покрытие сохранено.

### R2 — privileged netns harness

Коммит `432e33c` `ci(routing): execute privileged L11 netns gates on GitHub`.

- CI собирает оба тестовых бинаря обычным runner-пользователем и передаёт
  абсолютные пути через `LESHY_REGISTRY_TEST_BIN` /
  `LESHY_MONITOR_PROBE_BIN`.
- Добавлен шаг проверки: список `--list --ignored` обязан содержать все
  шесть обязательных имён, иначе job падает.
- `tests/linux-route-registry-netns.sh` стал root-only и без cargo:
  проверяет uid 0, наличие `ip`, существование и исполнимость обоих
  абсолютных путей, затем создаёт namespace и запускает бинари.
- `#[ignore]` сохранён как штатное скоупирование harness (план §5.4).

### R3 — targeted event path вместо full snapshot

Коммиты `a665505` (исходный) и `bcb9665`
`fix(routing): reconcile targeted Linux invalidations without full FIB snapshots`
+ `fix(routing): remove duplicated Linux observe_key from the R3 change`.

- `RouteBackend::observe_key` — таргетный read-back: Linux через
  `refresh_key`, Windows через `read_prefix`, дефолт fail-closed для
  AuthorityOnDemand/RegistryOnly.
- `RouteRegistry::mark_dirty` возвращает затронутые ключи; новый
  `reconcile_key` использует тот же `reconcile_entry` с точной проверкой
  identity (protocol/metric/scope/OIF/next-hop), что и full snapshot.
- `reconcile_after_invalidation`: `Full` — единственный полный снапшот;
  `Prefix`/`Interface`/`Zone` читают только свои ключи.
- `Interface` учитывает и intent-устройство, и записанный observation
  ifindex (у gateway-цели своего ifindex нет).
- Публичные счётчики `RouteManager::reconcile_counters()`
  (`full_snapshots`, `targeted_reads`); netns-тест IPv6-события проверяет,
  что `full_snapshots` не двигался.
- Новые тесты: `prefix_event_triggers_one_targeted_read_and_no_snapshot`,
  `interface_event_reads_back_only_the_affected_keys`,
  `zone_event_reads_back_only_that_zones_keys`,
  `full_invalidation_still_reads_exactly_one_full_snapshot`,
  `one_prefix_event_among_a_thousand_verified_keys_stays_flat`.

### R5 — Windows exact writer, observer и нативный гейт

Коммиты `fcf723f`
`fix(windows): refuse foreign same-prefix rows in the exact writer` и
`70c9839` `test(windows): add native route-registry acceptance`.

Статический аудит `src/routing/windows.rs` после правки:

~~~text
CreateIpForwardEntry2  -> только create_route (одна желаемая строка)
DeleteIpForwardEntry2  -> только delete_route (поиск живой строки по
                          записанной identity) и ветка §49.7 (единственная
                          записанная owned-строка)
find_route_rows(_mib)  -> read-only, один ключ (ip, prefix_len)
широкого удаления всего префикса нет; add_route/replace_route/
observed_to_mib_row удалены
~~~

- `create_route` больше не удаляет: EEXIST — успех только при точном
  совпадении identity, иначе `RouteConflict`.
- `ensure_route`: точная identity = no-op; одна записанная owned-строка
  может быть заменена; >1 строки или чужая строка = conflict.
- `delete_route` ищет живую строку по записанной identity и удаляет
  именно её; при отсутствии/смене identity ничего не удаляет.
- `NotifyRouteChange2` зарегистрирован, публикует targeted `Prefix`-
  инвалидацию (generation инкрементится до публикации), `Drop` отменяет
  регистрацию. TODO L12 закрыт.
- `gateway_of_another_family_is_rejected_by_row_construction` — отказ на
  уровне конструктора строки, не только валидации конфига.
- `tests/windows_route_registry.rs` + `windows/tests/route-registry.ps1`:
  9 runtime-кейсов, harness требует администратора, два разных адаптера,
  отказывается работать на адаптере с default route, снимает снимок
  посторонних маршрутов до/после и падает при любом отличии.

### R7 — транзакционный reload L14

Коммит `0da1b9f` `fix(reload): make exact route retirement fail closed`.

- Ошибка cleanup удалённой зоны и ошибка retirement изменённой зоны
  отменяют `apply_reload` до свопа конфига/матчера, с контекстом по зоне.
- `RouteRegistry::retire_intent -> Result<RetireOutcome{Retired,
  ReplacedByNewerIntent,Absent}>`; при ошибке desired и ownership
  сохраняются.
- `RouteManager::retire_intents -> Result<RetirementReport{retired,
  skipped_replaced}>`; `DnsHandler::retire_zone_intents` пробрасывает.
- Retry-задача статических маршрутов по-прежнему стартует только после
  commit.
- Тесты: 4 restart-only (R1) + `removed_zone_cleanup_failure_aborts_reload`,
  `changed_zone_retirement_failure_aborts_reload`,
  `replacement_static_route_failure_aborts_before_retirement`,
  `managed_reload_without_changes_sends_no_controller_rpc`.

### R8–R11 — журнал, pin, пакеты, статусы

- `fe06b13`: исторический префикс журнала (870 строк, Stage 0 … P0)
  возвращён из `9082031^`; записи после усечения сохранены как есть,
  добавлены разделитель и корректирующие записи R5–R7.
- `3b8859c`: `LESHY_REF` — полный 40-hex SHA; job разрешения pin
  отвергает ветки, короткие SHA и пробелы; оба пакетных job’а печатают
  `git rev-parse HEAD` и сверяют с pin.
- github run 42678886758: Resolve companion pin, Linux deb и
  Windows MSI + zip — success; в логах обоих пакетных job’ов
  `leshy pinned SHA: 0da1b9ffe51f891b41b0c2bc843b6d9250824a49`.
- `762a91e`: §55 parent packet — L11–L14 ACCEPTED с точными SHA и run ID,
  rescue definition-of-done закрыт.

## 3. Нативные гейты (лабораторные ВМ)

| ВМ | VirtualBox / адрес | Что запускалось | Результат |
|---|---|---|---|
| Ubuntu 26.04, ядро 7.0.0-42 | `ubuntu2604`, 192.168.3.68 | netns harness на `bcb9665` и на `0da1b9f` | 4 registry + 2 probe passed, 0 failed |
| Windows 10 19045 | `win10`, 192.168.3.69 | `windows/tests/route-registry.ps1`, адаптеры `kk-awg0` (ifIndex 14) и `Ethernet` (ifIndex 8) | 9/9 passed, снимок посторонних маршрутов без изменений |

Бинари для Windows-гейта собирались из чистого worktree на точный SHA
(`70c9839` и `0da1b9f`) и копировались на гостя; cargo на госте не нужен.

## 4. Таблица гейтов §17

| Gate | Команда/job | Результат | Доказательство |
|---|---|---|---|
| fmt | `cargo fmt --all -- --check` | PASS | локально, каждый этап |
| clippy | `cargo clippy --all-targets --all-features -- -D warnings` | PASS | локально, каждый этап |
| tests | `cargo test --all-targets --all-features` | PASS | 230/235 lib + 6 + 4 + 6 (R7) |
| release build | `cargo build --release` | PASS | локально |
| Linux target | `cargo check --target x86_64-unknown-linux-musl --all-targets --all-features` | PASS | локально |
| Ubuntu test | GitHub job | PASS | run 42677847701 |
| macOS test | GitHub job | PASS | run 42677847701 |
| Windows test | GitHub job | PASS | run 42677847701 |
| Linux integration | GitHub job | PASS | run 42677847701 |
| macOS integration | GitHub job | PASS | run 42677847701 |
| privileged netns | GitHub job | PASS | run 42677847701, job 129352953173 (R3 SHA) |
| Windows native route gate | lab VM | PASS | 9/9, `70c9839` и `0da1b9f` |
| Kikimora Build packages Linux | GitHub job | PASS | run 42678886758, job 129367808187 |
| Kikimora Build packages Windows | GitHub job | PASS | run 42678886758, job 129367808027 |

## 5. Инциденты и как они закрыты

1. **GitHub Actions терял job’ы.** У run 42672226556 не создались
   macOS build и Windows test, у первого прогона 42673966379 — пять
   job’ов; страница run отдавала internal server error. Перезапуск того
   же SHA (attempt 2) дал полные 8/8. Оба случая зафиксированы в журнале.
2. **Дубль `observe_key` в linux-only модуле.** Коммит `a665505` содержал
   две копии функции; Windows-хост не компилирует
   `#[cfg(target_os = "linux")]`, поэтому локальные гейты прошли, а
   поймал Linux CI. Исправлено `bcb9665` (6 строк удаления), после чего
   добавлена локальная проверка `cargo check --target
   x86_64-unknown-linux-musl`.
3. **Диск Ubuntu VM был заполнен (16 ГБ, 0 свободно).** Очищены кеши
   сборок, apt и старые .deb; данные продукта не трогались. После
   очистки netns harness прошёл.

## 6. Чего в rescue не делалось

- L15 не начат, K17 не начат, v0.6.0 не создавался, версия installer’а
  не менялась.
- Архитектура managed routing не менялась: AF_UNIX остаётся, локальный
  writer fallback не возвращался, exact ownership не ослаблялся.
- Ни один обязательный тест не удалён, не закомментирован и не помечен
  `#[ignore]`; новых `#[ignore]` нет, кроме нового Windows-runtime
  harness (та же схема скоупирования, что у Linux netns).
- CI не ослаблялся: нет `continue-on-error`, job’ы не удалялись,
  триггеры не сужались.

## 7. Ссылки

- План: `docs/toad-steps/08d1-post-l11-rescue-plan.md`
- Журнал: `docs/toad-steps/08d1-execution-log.md` (разделы R1–R4, R5–R7,
  R9–R11)
- Parent packet: `docs/toad-steps/08d1-managed-selected-routes-completion.md`,
  §52 (аудит), §55 (итоговые статусы)
- Leshy: `src/routing/{mod,registry,backend,linux,windows,server}.rs`,
  `tests/{linux_route_registry_netns,windows_route_registry}.rs`,
  `windows/tests/route-registry.ps1`, `.github/workflows/ci.yml`
