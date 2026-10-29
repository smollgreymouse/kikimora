# Модель безопасности Kikimora (2026-10-28)

Фиксирует принятые решения по независимому аудиту
(`independent-install-audit-2026-10-28.md`) и отложенные дизайны.

## Кто может говорить с ядром

- **Сокет**: Windows — AF_UNIX `C:\ProgramData\Kikimora\core.sock`;
  Linux — `/run/kikimora/core.sock` (0660, root:kikimora, каталог 0750).
- **Linux-авторизация**: uid 0, тот же uid, членство в группе `kikimora`
  (`peer_linux.go`). Деб не знает, какой аккаунт ходит в десктопный
  сеанс, поэтому постинст печатает подсказку
  `sudo usermod -aG kikimora $USER`; UI теперь объясняет EACCES этой
  подсказкой вместо голого «Unavailable».
- **Windows-авторизация (принятое решение)**: каталог ProgramData
  ограничивает доступ к пути сокета, но сам сокет открыт
  BUILTIN\Users на RW (`transport_windows.go`), а `authorizePeer`
  пуст — **любой локальный процесс может вести control-plane**
  (ConnectAll/RemoveTunnel/роутинг). Это осознанный компромисс
  однопользовательского десктоп-продукта: секретов в снапшоте нет,
  файлов инъекцией не создать (TOML на диске принадлежит
  SYSTEM/Administrators). **Дорожная карта**: именованный пайп с
  DACL на SID устанавливающего пользователя + токен подтверждения на
  каждое соединение — если продукт пойдёт в многопользовательские
  инсталляции (терминальные серверы).

## Файлы и права

- `%ProgramData%\Kikimora\{state,toads,logs,leshy}` — SYSTEM +
  Administrators только: `icacls /inheritance:r` в install.ps1
  (портативный путь) И в четырёх deferred custom actions Harden*Dir
  внутри MSI (аудит 2026-10-28, второй прогон: PermissionEx только
  добавляет ACE — наследуемый Users-read остался бы). **Raw SIDs
  обязательны** (S-1-5-18, S-1-5-32-544): на локализованной Windows
  имена («Administrators») не резолвятся; и один icacls-вызов на
  каталог — multi-path quoted строка отвергается. Проверено на ru-RU
  win10-ВМ. TOML-роли содержат приватные ключи AWG и
  `endpoint_policy.command`, исполняемый от SYSTEM; из control-сокета
  эта команда недостижима (TunnelSpec не имеет такого поля).
- Deb-путь: `/etc/kikimora/{toads,secrets}` root-only (установщик),
  конфиги Leshy root-owned 0644/0600.

## Осознанные ограничения v1 (второй прогон аудита, 2026-10-28)

1. **Windows shutdown-бюджет**: 20с последовательного ReleaseProduct;
   при нескольких медленных ролях процесс может быть убит посреди
   релиза — fail-closed парки переживут до следующего старта ядра.
   Restart-marker контракт на Windows не активен: обычный stop всегда
   освобождает сеть (fail-open) — безопасно, но park-preserving
   рестарт существует только на Linux.
2. **OpenConnect server-pushed DNS** публикуется в
   openconnect-network.env, но не применяется: корпоративные внутренние
   имена резолвятся только после ручных зон. Хост-DNS при этом не
   мутируется (fail-safe).
3. **MSI несёт leshy-win** (исправлено 2026-10-28 вечером): при сборке со
   stage.ps1 -LeshyDir <portable> MSI ставит leshy.exe в
   `Program Files\Kikimora\leshy\`, регистрирует сервис `leshy`
   (auto, аргументы `--service leshy C:\ProgramData\leshy\config.toml`),
   ServiceControl Start=both перезапускает и предсуществующий Leshy-сервис
   (Vital=no — конфликт имён не роняет установку). uninstall удаляет
   сервис только если его binPath указывает в наш install-root.
4. **Лог ядра без ротации** (`logs\kikimora-core.log`, O_APPEND).
5. **Depends deb тянет NetworkManager** даже на NM-less системах.

## Честные компромиссы (зафиксировано, в роадмапе)

1. **OpenConnect-провижининг**: бинарь openconnect.exe не поставляется;
   https-импорт без username/credential-файла отвергается ядром с
   внятной ошибкой в импорт-шите. Полное закрытие — drawer ввода
   кредов + скачивание бинаря (отдельная задача).
2. **Композиция только после рестарта** (Create/RemoveTunnel): ядро
   честно отвечает «restart the core to load it»; в UI сигнал живёт в
   импорт-шите. Полное закрытие — live-пересборка композиции в ядре.
3. **Windows named-pipe auth** — см. выше.
4. **`--fake` остаётся в релизном бинарнике** (тесты/диагностика);
   включённый фейк помечен баннером SIMULATED во всех поверхностях.
