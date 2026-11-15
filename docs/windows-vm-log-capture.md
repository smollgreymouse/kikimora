# Capturing output from Windows lab-VM runs

Why this exists: across three separate sessions (2026-11-12, 11-13, 11-15) the
win10 guest came back with an empty or missing log, and each time real time went
into guessing whether the gate failed, hung, or never started. Every claim below
was measured on the actual `win10` guest (`192.168.3.86`, Windows 10 19045,
OpenSSH_for_Windows 9.5, `greymouse`, PowerShell 5.1 era `powershell.exe`) during
the 2026-11-15 C8 session. None of it is inferred from documentation.

Read `lab-vms.md` first for address resolution and the transport helper; this
file is only about getting the text out.

## The one rule

Capture inside the guest, in a file, on the guest's own filesystem. Never rely
on the SSH channel to carry a long run's output.

`python3 scripts/lab/vm.py run windows "..."` uses paramiko `exec_command` and
returns only what arrives before the channel closes. A privileged Windows gate
(MSI install, service start, leshy restart) routinely outlives that window, and
one 2026-11-15 run died with `TimeoutError` in `channel.recv` while the gate was
still running on the guest. The output of that run was unrecoverable because
nothing had been written to disk.

## What actually works

Measured matrix. The subject was a 12-second script writing six lines, so that
"streams during the run" and "only at the end" are distinguishable.

| Method | Bytes during run | Bytes after exit | Verdict |
|---|---:|---:|---|
| `Start-Process -RedirectStandardOutput` (no `-Wait`) | 51 | 102 | works, streams |
| `Start-Process -RedirectStandardOutput -Wait` | n/a | 102 | works |
| `cmd /c "... > file 2>&1"` | 353 | 353 | works, streams |
| `& { ... *>&1 } \| Tee-Object -FilePath file` | n/a | 1416 | works |
| `Start-Transcript -Path file` | n/a | 1991 | works |
| gate's own `$env:TEMP` logs | n/a | yes | works, see below |

Two conclusions that cost the most time:

1. **`Start-Process -RedirectStandardOutput` is not the problem.** An earlier
   reading in the same session concluded it "produces 0 bytes on this host". That
   was wrong: the child had died instantly with
   `File ... cannot be loaded because running scripts is disabled on this system`,
   so there was nothing to capture. `-ExecutionPolicy Bypass` was missing. A
   redirect that yields an empty file is evidence about the *child*, not about
   the redirect — always read the `.err` stream before blaming the mechanism.

2. **`Start-Transcript` does not capture `& child.ps1` output when the child
   fails to load**, but it does record the error, so the transcript is still the
   better first artifact: 1991 bytes with the real `SecurityError` inside.

## The recipe to use

Write a `.cmd` wrapper next to the script and start it with `cmd /c`. This is the
only form that survived being quoted through three layers
(SSH → `powershell -Command` → child) and that streams while it runs.

```bat
@echo off
cd /d C:\Users\greymouse\Desktop
powershell -NoProfile -ExecutionPolicy Bypass -File "C:\Users\greymouse\Desktop\test_state_layout_upgrade.ps1" ^
  -Msi "C:\Users\greymouse\Desktop\kikimora-1.3.1-windows-amd64.msi" ^
  -FailpointCore "C:\Users\greymouse\Desktop\kikimora-core-failpoints.exe" ^
  -Leshy "C:\Users\greymouse\Desktop\leshy.exe" ^
  > "C:\Users\greymouse\Desktop\upg.log" 2>&1
echo EXITCODE=%ERRORLEVEL% >> "C:\Users\greymouse\Desktop\upg.log"
```

Upload and start it:

```bash
python3 scripts/lab/vm.py put windows run-upgrade.cmd C:/Users/greymouse/Desktop/run-upgrade.cmd
python3 scripts/lab/vm.py run windows \
  "powershell -NoProfile -Command \"Start-Process cmd -ArgumentList '/c','C:\\Users\\greymouse\\Desktop\\run-upgrade.cmd' -WindowStyle Hidden -PassThru | Select-Object -ExpandProperty Id\""
```

Then poll without touching the run:

```bash
python3 scripts/lab/vm.py run windows \
  "powershell -NoProfile -Command \"(Get-Item 'C:\\Users\\greymouse\\Desktop\\upg.log').Length; Get-Content 'C:\\Users\\greymouse\\Desktop\\upg.log' -Tail 10\""
```

`EXITCODE=` in the tail is the authoritative success signal; the log text alone
is not, because `ErrorActionPreference = "Continue"` lets a gate keep going after
a failure line.

Do not invoke the `.cmd` by path via `Start-Process 'x.cmd'` — that form produced
no log at all on this guest, while `cmd /c x.cmd` streamed immediately.

## Always check the gate's own logs too

`test_state_layout_upgrade.ps1` and `test_managed_cutover.ps1` write their own
artifacts to `$env:TEMP`, not to the Desktop, and those are the ones that contain
the real diagnosis:

```text
%TEMP%\kikimora-upgrade-install.log        MSI /l*v transcript (multi-MB)
%TEMP%\kikimora-e-install.log              same, for the cutover gate
%TEMP%\kikimora-upgrade-console.log        failpoint core stdout
%TEMP%\kikimora-upgrade-console.err.log    failpoint core stderr  <-- the cause
```

A wrapper log showing only `== pre-clean: uninstall previous MSIs ...` and then
nothing was, on 2026-11-15, a gate that had already died; the reason was sitting
in the console err log:

```text
kikimora-core: endpoint policy set: endpoint policy for "upgradegate" requires zone and positive rule_priority
```

Enumerate them with:

```bash
python3 scripts/lab/vm.py run windows \
  "powershell -NoProfile -Command \"Get-ChildItem \$env:TEMP -Filter 'kikimora-*' | Sort-Object LastWriteTime -Descending | Select-Object -First 8 Name,Length,LastWriteTime | Out-String\""
```

`$env:TEMP` expands to `C:\Users\greymouse\AppData\Local\Temp`.

## Distinguishing the three failure shapes

| Symptom | Meaning | Next check |
|---|---|---|
| log file absent | process never started, or the path never reached `cmd` | `(Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" \| Where-Object { $_.CommandLine -like '*<script>*' } \| Measure-Object).Count` |
| file exists, 0 bytes | child died before writing; or wrong quoting layer | read the `.err` stream; add `-ExecutionPolicy Bypass` |
| file stops mid-row | gate still running (normal) or genuinely hung | `msiexec`/`kikimora-core` process count; `Get-ChildItem C:\ProgramData\Kikimora` |

The mid-run truncation seen repeatedly is usually just the run being long: the
state-layout upgrade gate takes well over 15 minutes on this guest for three
rows, and each row uninstalls and reinstalls the MSI. Poll, do not re-run.

## Quoting

Three quoting layers is where real time is lost. Rules that held up:

- put logic in an uploaded file; pass only plain paths on the command line;
- prefer `--host` and a `.cmd`/`.ps1` file over long inline expressions;
- backticks and `$(...)` inside `powershell -Command "..."` through SSH break
  unpredictably; `-replace "\`r?\`n"` was outright rejected, while the same
  operation in an uploaded script worked;
- for text with `$` inside a double-quoted SSH command, escape as `\$`;
  `Out-String` beats `Format-Table` when the output is going to be read by a
  human or a parser.

## Evidence hygiene

These guests carry VPN role secrets (`C:\ProgramData\Kikimora\toads\*.toml`,
`/etc/kikimora/toads/*.toml`). When a log or config must be quoted in a
repository document, mask `private_key`, `preshared_key` and `peer_public_key`
first. Never upload a role file into the repo; upload it straight to the guest.
