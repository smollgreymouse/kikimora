# Kikimora Chrome extension

Manifest V3 extension for managing Kikimora `primary` and `secondary` domain
lists directly from Google Chrome.

The popup can:

- prefill the domain from the active HTTP/HTTPS tab;
- add it to `primary` or `secondary`;
- show both configured domain lists;
- filter visible domains;
- remove a domain from its current zone.

## Architecture

```text
Chrome popup
    |
    | chrome.runtime.sendNativeMessage()
    v
com.kikimora.domain_manager
    |
    +-- list --> /usr/local/sbin/kikimora domains list ZONE
    |
    +-- add/remove --> pkexec /usr/local/sbin/kikimora domains ...
```

There is no localhost HTTP server and no listening network port. Chrome starts
the native host only for a request from the extension. The native host manifest
allows exactly this extension ID:

```text
amllchapajpfdibbngeghpjbbofemaif
```

The extension ID is stable because `manifest.json` contains a fixed public key.
The native host validates the caller origin, domain syntax, zone, and action.

Reading the lists uses the unprivileged Kikimora CLI. Adding or removing a domain
uses `pkexec`, because the configuration belongs to root. Kikimora's existing CLI
performs the transactional list update, rebuilds and validates the Leshy
configuration, rolls back on failure, and restarts Leshy when necessary.

## Install

The Chrome integration is an optional component. A normal Kikimora installation:

```bash
sudo ./install.sh
```

does **not** install browser files, the native messaging host, or Chrome
registrations.

Kikimora must already be installed at `/usr/local/sbin/kikimora`. The desktop
must provide `python3` and `pkexec`. From the root of the extracted Kikimora
package run the dedicated installer command:

```bash
./browser/chrome/test.sh
sudo ./install.sh chrome-extension
```

This command installs only the browser integration; it does not reinstall,
upgrade, or reconfigure Kikimora itself.

The lower-level script `sudo browser/chrome/install.sh` performs the same
Chrome-only installation, but the top-level command is the supported entrypoint.

Then in Google Chrome:

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked**.
4. Select `/usr/local/share/kikimora/chrome-domain-extension`.
5. Pin **Kikimora Domain Router** to the toolbar.

The same system-wide native host registration is installed for Google Chrome,
Google Chrome for Testing, and Chromium.

## Use

Open the extension to see the `primary` and `secondary` lists immediately. Use
the filter field to find a domain in long lists.

To add the current site, choose **Primary** or **Secondary** and press **Add
domain**. The domain field remains editable before submission.

To remove an entry, press **Delete** next to it and confirm the action. Add and
remove operations show a PolicyKit authorization dialog; list viewing does not.

Adding a domain that is already in the other zone is rejected by Kikimora's
configuration validation rather than silently moving it. Remove it from the old
zone first.

## Uninstall

```bash
sudo browser/chrome/uninstall.sh
```

Then remove the unpacked extension from `chrome://extensions`.
