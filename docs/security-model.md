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
  Administrators только (`icacls /inheritance:r` в install.ps1);
  TOML-роли содержат приватные ключи AWG и `endpoint_policy.command`,
  исполняемый от SYSTEM.
- Deb-путь: `/etc/kikimora/{toads,secrets}` root-only (установщик),
  конфиги Leshy root-owned 0644/0600.

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
