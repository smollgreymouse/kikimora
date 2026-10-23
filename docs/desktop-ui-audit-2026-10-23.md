# UI review: two-page Kikimora (2026-10-23)

## Scope

Current development branch: `feat/amnezia-inspired-ui`, rebased by fast-forward on
`origin/feat/amnezia-inspired-ui` at `35576a0`.

The product has exactly two navigable pages, **Home** and **Settings**. A role
details sheet and a tunnel import sheet remain overlays, not pages.

## Changes in this pass

- Home has one clearly labelled, themed profile ComboBox and an adjacent
  create-profile button. Removed the duplicate clickable title/menu.
- Created a modal profile-name dialog with duplicate/empty-name guard at
  submit time.
- Home role list filters by known profile membership, preserving the original
  role index for actions. Unknown membership is deliberately shown rather than
  silently excluding roles (compatibility with older cores).
- Filtered model identity stays stable across frequent core snapshots, to avoid
  destroying delegates and stealing clicks while a role is being opened.
- A role row opens a bottom sheet. Its principal sections are **Settings**
  (protocol, server, interface, profile membership editor) and **Routes**
  (editable domain/CIDR list, validation, save). Diagnostic fields are collapsed
  behind an explicit disclosure.
- Fixed the shared `KRoleList` delegate's zero-height row container (previously
  rendered, but hit testing missed it); enabled reliable row pointer delivery.
- Added product-styled profile selectors and fixed light/dark dropdown
  contrasts and navigation glyphs.
- Settings now leads with appearance and desktop integration, with advanced
  diagnostics collapsed at the end. Removed the orphan `DiagnosticsPage` from
  the visible root layout.
- Fixed a dead QML routing-list change callback: `CoreBackend` signals
  `snapshotChanged`, not `routingListsChanged`. Route dirty checks compare
  normalized content and reject malformed numeric IPv4/CIDR entries.
- The Go core now publishes `profiles` per role in snapshots. The Qt real
  client consumes them instead of relying only on membership commands issued
  during the current UI session. Versioned older core snapshots remain
  compatible.
- Extended UI behavior tests for profile creation/switching, membership
  filtering, role opening, route saving and route validation; added Go snapshot
  membership assertion.

## Verification

- Linux Qt offscreen CTest: 9/9 desktop targets green.
- Go: `go test ./internal/control ./internal/config` green from `toad/`.
- Snapshot render verified for 360x720 in dark and light themes and the settings page.
- Windows VM was **not** re-run in this pass; Windows acceptance still needs an
  interactive smoke test for native window decorations and popup positioning.

## Current backend/API gaps (do not hide in the UI)

1. **Tunnel connection parameters cannot yet be edited.** The IPC contract
   currently offers `CreateTunnel`, `RemoveTunnel`, `SetTunnelProfiles`
   and routing-list changes, but no safe secret-preserving read/patch/update
   configuration flow. The sheet displays connection parameters read-only and
   says so. This requires a separate `GetTunnelSettings/UpdateTunnelSettings`
   contract, revision/concurrency checks, atomic saves, validation and
   transition/restart semantics before editing a gateway, UUID, SNI or
   credentials in the GUI.
2. **Profile creation is not truly empty in the Go core.** Its current
   `CreateProfile` path adds the new name to all tunnel configuration files,
   because profiles are derived from membership. For the expected independent
   empty-profile UX, persist profile definitions independently of tunnel
   membership and include them in snapshots. Do not try to paper over this
   with client-side filtering.
3. **Configuration changes versus live state.** The current daemon loads the
   composition at construction. Creating/moving a tunnel/profile writes disk
   state and needs an explicit runtime reload/restart path to become active.
   Avoid implying that saved connection configuration is already live.
4. **Uncommitted route drafts.** The editor protects drafts from periodic
   snapshots when possible, but moving between roles/closing the sheet has no
   confirm-discard dialog yet. Add one with correct modal/overlay semantics.
5. **Windows UI smoke:** verify theme persistence, Combobox popups, role sheet
   gestures/scrolling, import and profile dialog focus using the real Windows
   build (not just Linux Qt offscreen).

## Next UX acceptance criteria

- Choose profile A and see only its roles; choose B and see B's roles, including
  newly imported ones after a backend resync.
- Create an empty profile without cloning every role; add/move one role into it,
  persist this across restart and verify the snapshot's profile membership.
- Edit a role's non-secret connection settings through validated read/patch
  IPC, with conflict/restart handling and no secret leakage.
- The Routes subtab supports comments, domains, IPv4/IPv6 CIDRs, save-error
  display, discard confirmation and confirmed round-trip readback.
- 360x620 and 360x720, dark/light, Linux and Windows screenshots show no
  clipped navigation, title or action buttons.
