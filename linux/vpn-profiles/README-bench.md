# Standalone VPN benchmarks

These scripts are intentionally separate from Kikimora runtime and are not installed or invoked by Kikimora.

Run manually from this directory after switching VPN modes yourself:

```bash
bash bench-wg2-kikimora.sh
bash bench-wg2-clean.sh
bash bench-xray-clean.sh
```

Results are written to `./vpn-bench-results/` by default.

Optional environment overrides:

```bash
RUNS=20 HTTPS_URL=https://www.google.com/generate_204 DOWNLOAD_URL='https://speed.cloudflare.com/__down?bytes=10000000' PING_HOST=1.1.1.1 bash bench-xray-clean.sh
```

The scripts only collect diagnostics and traffic measurements. They do not start, stop, reconfigure, or modify Kikimora or AmneziaVPN.
