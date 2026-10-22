# Linux Wi-Fi soft-rfkill incident: causal attribution and recovery

Status: **investigation / regression proposal**, not a proven Kikimora, Leshy, Amnezia or NetworkManager defect.
Observed on a machine running the legacy stack, 2026-10-22.

## Reported evidence

At approximately 09:44:18 and 14:30:54, `wpa_supplicant` logged
`CTRL-EVENT-DISCONNECTED ... reason=3 locally_generated=1`, immediately followed by
`rfkill: WLAN soft blocked` on `wlp0s20f3`.
Amnezia firewall helper repeatedly reported
`iptables v1.8.11 (nf_tables): Chain 'amnvpn.300.allowLAN' does not exist`.
A reboot restored connectivity.

**Do not infer causality from temporal proximity.** IEEE 802.11 reason code 3 means
station leaving/has left BSS/ESS; inactivity is code 4. The wpa_supplicant rfkill
message reports an observed rfkill state change, not proof that wpa_supplicant
caused the change. An absent iptables chain does not itself perform rfkill writes.

## Open hypotheses to test

1. NetworkManager radio-control (D-Bus / nmcli), desktop radio switch,
   rfkill helper, systemd-rfkill, ACPI/driver or another userspace process
   deliberately wrote a soft block.
2. The legacy Kikimora network watchdog / dispatcher / recovery actions,
   directly or indirectly, toggled Wi-Fi in response to transient underlay state.
3. Amnezia firewall initialization/reconciliation and legacy Kikimora/Leshy
   manipulated shared netfilter/routing state, producing a loop or loss of
   connectivity. This is a *different* failure mechanism from rfkill.
4. Leshy route/DNS update storms or VPN transport retry storms overloaded local
   networking / the home router. Neither would ordinarily issue rfkill directly.
5. Resume/power management, Wi-Fi firmware/driver, router/AP disassociation,
   or an external manager caused initial Wi-Fi loss; VPN errors were consequences.

## Non-destructive incident triage

Preserve journals from **both** boots, especially the previous boot after reboot.
No automatic restart/unblock, udev workaround, disabling VPN, or firewall flush
should be performed before evidence is collected.

```sh
journalctl --list-boots --no-pager
journalctl -b -1 -o short-precise --no-pager > previous-boot.log
journalctl -b -1 --since '2026-10-22 14:30:30' --until '2026-10-22 14:31:30' \
  -o short-precise --no-pager
journalctl -b -1 --no-pager | grep -Ei \
  'rfkill|radio-control|wireless-enabled|NetworkManager|wpa_supplicant|Amnezia|kikimora|leshy|dispatcher|systemd-rfkill'
rfkill list
nmcli radio
nmcli -f GENERAL.STATE,GENERAL.CONNECTION,GENERAL.DEVICE device show wlp0s20f3
systemctl list-units --all '*amnezia*' '*kikimora*' '*leshy*'
```

Check `NetworkManager` audit messages for `op="radio-control"` and
`arg="wireless-enabled:off"` with PID/UID, kernel rfkill events, sleep/resume
and the *first* causal event. A reboot may clear state but does not identify
the actor. If historical logs are absent, arrange a bounded, privacy-conscious
future trace of NetworkManager radio control, rfkill writes and systemd units;
avoid claiming a writer from wpa_supplicant's observation alone.

For a possible flood, record rates per minute (never confuse log spam with
network packet spam): process fork/exits, firewall operations and their errors,
route/link churn, netlink updates, DNS query/retry rates, external endpoint
reconnect attempts and router/AP log counters. Distinguish local CPU/journal
overload from traffic actually crossing the physical interface.

## Core behavior and acceptance

- G2: observe `rfkill` hard/soft state and physical-link/NetworkManager state
  as **diagnostic inputs**, separating radio disabled from an underlay
  unavailable for unrelated reasons; coalesce duplicate events.
- G7: on physical-underlay loss transition roles to `WaitingForUnderlay`
  while keeping fail-closed parking. Do not blindly restart VPN or repeat
  firewall setup. When radio recovers, reconcile per role against the new
  underlay epoch; validate/rebind/restart only when needed.
- G10: report cause and evidence, e.g. interface name, soft/hard block,
  NM radio enabled, last transition, underlay epoch, per-role actions and
  limited event counters. Attribute PID/UID only where actually supported
  by audit evidence.
- Add rootless unit tests for event storms and blocked/unblocked transitions,
  and privileged Linux regression tests for real rfkill/network changes
  where test environment permits. Never modify the host's physical radio in
  unattended CI.

### Safety invariants

Kikimora must not automatically issue `rfkill unblock wifi`, undo explicit
user airplane-mode intent, set unconditional udev unblock rules or restart the
entire VPN stack on connectivity-only NetworkManager changes. Linux recovery
must be bounded and idempotent, avoiding retry storms and preserving the
single-writer ownership of firewall/routes.

## Evidence gate

This issue is **not closed** by a reboot, a speculative Amnezia explanation,
or passing rootless tests. Closing requires reproducible attribution of
the soft-rfkill writer or justified classification as unresolved, plus
rate-limited recovery tests and evidence from the affected Linux system.

## Follow-up: previous-boot logs inspected over CTUN (2026-10-22)

The original RFKill actor is now partly attributed, correcting the earlier hypothesis:

- Both 09:44:18 and 14:30:54 NetworkManager audit entries show `op="radio-control" arg="wireless-enabled:off" pid=8020 uid=1000 result="success"`.
- `journalctl -b -1 _PID=8020` identifies PID 8020 as **`/usr/bin/gnome-shell` (GNOME Shell / Mutter 49.0)**, not Kikimora, Leshy or the Amnezia daemon. At 14:30:57 the same process requested `wireless-enabled:on`.
- At 14:30:54 the kernel logged local deauthentication (`Reason: 3=DEAUTH_LEAVING`); only then do supplicant and NetworkManager report blocked radio.
- At 14:30:48 and 14:30:52 Netplan/NetworkManager reported an invalid generated address `192.168.2.1/0` in `/etc/netplan/90-NM-1642677c-4daf-369b-91e9-105cf5c58c95.yaml`, near repeated NetworkManager reload requests. The provenance of that configuration and reload requests remains **unknown**.
- Leshy had large bursts of localhost DNS requests and emitted repeated upstream `No route to host`/`ServFail` failures against 1.1.1.1 and 8.8.8.8, including **before** the 14:30:54 Wi-Fi disable. This confirms noisy local failure behavior, not evidence of router overload or a Leshy-caused rfkill command.
- Amnezia's NetworkWatcher reacted to wireless state changes. Its prior firewall-chain errors and handshake polling are not evidence that it sent the radio-control D-Bus request.

**Revised attribution:** the direct NetworkManager D-Bus caller was GNOME Shell. Investigate GNOME Shell's Wi-Fi/airplane-mode UI, shell extensions, hotkeys, input devices and other radio-switch events; a triggered GNOME reaction still needs causal identification. Separately investigate invalid Netplan configuration, NetworkManager reload loop, and Leshy DNS request rates. Do not conflate those independent observations or make permanent host modifications without evidence.
