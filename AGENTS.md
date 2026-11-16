# Kikimora — agent/operator entry point

Read this file before touching the repository.

## 1. Current work authority

Start here:

~~~text
docs/roadmap.md
~~~

Current production-readiness audit and continuation packet:

~~~text
docs/toad-steps/08d1-prod-readiness-audit-2026-11-16.md
~~~

Do not infer the current stage from old execution-log entries. The execution
log is evidence/history; `docs/roadmap.md` owns current order and status.

At the 2026-11-16 audit the executor must start at **PROD1**, not U11.

## 2. Lab VM access — do not rediscover this

The canonical tracked helper is:

~~~text
scripts/lab/vm.py
~~~

Full contract:

~~~text
docs/lab-vms.md
~~~

Before any Ubuntu/Windows VM work, use:

~~~bash
python3 scripts/lab/vm.py discover ubuntu --source
python3 scripts/lab/vm.py discover windows --source
~~~

Run commands:

~~~bash
python3 scripts/lab/vm.py run ubuntu "<command>"
python3 scripts/lab/vm.py run windows "<command>"
~~~

Upload files:

~~~bash
python3 scripts/lab/vm.py put ubuntu <local> <remote>
python3 scripts/lab/vm.py put windows <local> <remote>
~~~

Address resolution is already implemented as:

~~~text
explicit --host
-> KIKIMORA_UBUNTU_VM_HOST / KIKIMORA_WINDOWS_VM_HOST
-> VirtualBox Guest Additions discovery on 192.168.3.0/24
-> documented fallback (Ubuntu .74, Windows .72)
~~~

Authentication is SSH key/agent first, then local `LAB_PW` fallback.
Ubuntu upload uses SCP-over-SSH because the guest SFTP subsystem has historically
been unreliable; Windows upload uses SFTP.

**Do not:**

- scan old execution logs to guess the current IP;
- create another `tmp/vm_run.py`, `win_ssh.py`, ad-hoc Paramiko helper or
  hard-coded SSH command before testing `scripts/lab/vm.py`;
- treat a fallback address as proof that the VM is powered on/reachable;
- commit passwords, keys, VPN credentials or GuestControl secrets.

Investigate transport only after the canonical helper itself returns a concrete
failure.

## 3. Managed-routing companion

The authoritative Leshy revision is always:

~~~text
.github/managed-routing-companion.env
~~~

Never replace it with a moving branch or an old `ftelnov/leshy` release for
acceptance. Current CI consumes the pinned `smollgreymouse/leshy` revision.

## 4. Repository hygiene

Before a stage:

~~~bash
git status --short --branch
git rev-parse HEAD
git rev-parse origin/master
cat .github/managed-routing-companion.env
~~~

Do not reset/rebase away local work. Do not stage unrelated user files.

Known unrelated user files that are not roadmap inputs:

~~~text
.gigacode/
docs/vbox-guest-network-analysis.md
kikimora1.png
kikimora2.png
kikimora3.png
kikimora4.png
~~~

Before every commit:

~~~bash
git diff --check
~~~

Do not weaken a failing acceptance gate to make it green. A VM-local patch or
an ad-hoc `/tmp` acceptance script is not release evidence when the roadmap
requires a tracked harness.
