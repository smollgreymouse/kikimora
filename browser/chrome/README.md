# Kikimora Chrome extension

Manifest V3 extension for adding the domain of the active Chrome tab to the
Kikimora `primary` or `secondary` domain list.

## Architecture

```text
Chrome popup
    |
    | chrome.runtime.sendNativeMessage()
    v
com.kikimora.domain_manager
    |
    | pkexec
    v
/usr/local/sbin/kikimora domains add DOMAIN --primary|--secondary
```

There is no localhost HTTP server and no listening network port. Chrome starts
the native host only for a request from the extension. The native host manifest
allows exactly this extension ID:

```text
amllchapajpfdibbngeghpjbbofemaif
```

The extension ID is stable because `manifest.json` contains a fixed public key.
The native host validates the caller origin, domain syntax, and zone. Kikimora's
existing CLI performs the transactional list update, rebuilds and validates the
Leshy configuration, rolls back on failure, and restarts Leshy when necessary.

## Install

Kikimora must already be installed at `/usr/local/sbin/kikimora`. The desktop
must provide `python3` and `pkexec`.

```bash
cd browser/chrome
./test.sh
sudo ./install.sh
```

Then in Google Chrome:

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked**.
4. Select `/usr/local/share/kikimora/chrome-domain-extension`.
5. Pin **Kikimora Domain Router** to the toolbar.

The same system-wide native host registration is installed for Google Chrome,
Google Chrome for Testing, and Chromium.

## Use

Open a normal HTTP or HTTPS page, click the extension, choose **Primary** or
**Secondary**, and press **Add domain**. The field is editable before submission.
A PolicyKit authorization dialog appears because Kikimora configuration is owned
by root.

Adding a domain that is already in the other zone is rejected by Kikimora's
configuration validation rather than silently moving it. Remove it from the old
zone first.

## Uninstall

```bash
sudo browser/chrome/uninstall.sh
```

Then remove the unpacked extension from `chrome://extensions`.
