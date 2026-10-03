# Kikimora macOS service adapter

The macOS core service is a launchd system daemon. The checked-in template is
`files/com.kikimora.core.plist`; install it as
`/Library/LaunchDaemons/com.kikimora.core.plist` with owner `root:wheel` and
mode `0644`, then load it with:

```sh
sudo launchctl bootstrap system /Library/LaunchDaemons/com.kikimora.core.plist
sudo launchctl enable system/com.kikimora.core
```

The Go adapter owns a kernel `utun` descriptor and reads the physical default
path through the BSD route database. It never changes the default route and
uses exact endpoint/parking operations only. Runtime validation must be done
on macOS with an authenticated administrator; Linux CI only verifies the
Darwin amd64/arm64 compilation boundary.
