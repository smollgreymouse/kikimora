use std::env;
use std::ffi::OsString;

use async_trait::async_trait;
use serde::de::DeserializeOwned;
use tokio::process::Command;

use super::{Backend, BackendError, ServiceAction};
use crate::models::{DnsResponse, EndpointsResponse, LogsResponse, ProfilesResponse, StatusResponse};

#[derive(Clone, Debug)]
pub struct LinuxBackend {
    cli: OsString,
}

impl Default for LinuxBackend {
    fn default() -> Self {
        Self {
            cli: env::var_os("KIKIMORA_CLI").unwrap_or_else(|| OsString::from("kk")),
        }
    }
}

impl LinuxBackend {
    async fn json<T>(&self, command: &str, extra: &[String]) -> Result<T, BackendError>
    where
        T: DeserializeOwned,
    {
        let mut process = Command::new(&self.cli);
        process.arg(command).arg("--json").args(extra);
        let output = process.output().await.map_err(|error| {
            BackendError::new(format!("failed to execute kk {command} --json: {error}"))
        })?;
        if !output.status.success() {
            return Err(Self::command_error(command, &output.stderr));
        }
        serde_json::from_slice(&output.stdout).map_err(|error| {
            BackendError::new(format!("invalid JSON from kk {command} --json: {error}"))
        })
    }

    async fn run_cli(&self, args: &[&str]) -> Result<(), BackendError> {
        let output = Command::new(&self.cli)
            .args(args)
            .output()
            .await
            .map_err(|error| BackendError::new(format!("failed to execute kk: {error}")))?;
        if output.status.success() {
            return Ok(());
        }
        Err(Self::command_error(&args.join(" "), &output.stderr))
    }

    fn command_error(command: &str, stderr: &[u8]) -> BackendError {
        let detail = String::from_utf8_lossy(stderr).trim().to_owned();
        if detail.is_empty() {
            BackendError::new(format!("kk {command} failed"))
        } else {
            BackendError::new(format!("kk {command} failed: {detail}"))
        }
    }
}

#[async_trait]
impl Backend for LinuxBackend {
    async fn status(&self) -> Result<StatusResponse, BackendError> {
        self.json("status", &[]).await
    }

    async fn profiles(&self) -> Result<ProfilesResponse, BackendError> {
        self.json("profiles", &[]).await
    }

    async fn endpoints(&self) -> Result<EndpointsResponse, BackendError> {
        self.json("endpoints", &[]).await
    }

    async fn dns(&self) -> Result<DnsResponse, BackendError> {
        self.json("dns", &[]).await
    }

    async fn logs(&self, lines: usize) -> Result<LogsResponse, BackendError> {
        self.json("logs", &["--lines".to_owned(), lines.to_string()])
            .await
    }

    async fn service(&self, action: ServiceAction) -> Result<(), BackendError> {
        self.run_cli(&[action.as_cli_arg()]).await
    }

    async fn use_profile(&self, name: &str) -> Result<(), BackendError> {
        self.run_cli(&["profiles", "use", name]).await
    }

    async fn set_dns(&self, use_leshy: bool) -> Result<(), BackendError> {
        self.run_cli(&["dns", if use_leshy { "enable" } else { "disable" }])
            .await
    }

    async fn set_startup(&self, enabled: bool) -> Result<(), BackendError> {
        self.run_cli(&[if enabled { "enable" } else { "disable" }])
            .await
    }

    async fn rediscover_endpoints(&self, role: &str) -> Result<(), BackendError> {
        self.run_cli(&["endpoints", "rediscover", role]).await
    }

    async fn invalidate_endpoint_cache(&self, role: &str) -> Result<(), BackendError> {
        self.run_cli(&["endpoints", "invalidate", role]).await
    }
}

#[cfg(test)]
mod tests {
    use std::fs;
    use std::os::unix::fs::PermissionsExt;
    use std::path::PathBuf;
    use std::process;
    use std::time::{SystemTime, UNIX_EPOCH};

    use super::*;

    fn fake_cli(script_body: &str) -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock before unix epoch")
            .as_nanos();
        let path = env::temp_dir().join(format!("kikimora-core-{}-{nonce}.sh", process::id()));
        fs::write(&path, script_body).expect("write fake kk");
        let mut permissions = fs::metadata(&path).expect("stat fake kk").permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(&path, permissions).expect("chmod fake kk");
        path
    }

    fn contract_cli() -> PathBuf {
        fake_cli(
            r#"#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "${0}.log"
case "${1:-}" in
  status)
    [[ "${2:-}" == "--json" ]] || exit 20
    cat <<'JSON'
{"schema_version":1,"service":"running","services":{"leshy":"running","route_watch":"running","health_watch":"stopped"},"profiles":{"active":"office"},"interfaces":{"primary":{"name":"amn0","state":"ready"},"secondary":{"name":"tun0","state":"ready"},"dns":{"name":"leshy-dns0","state":"active"}},"dns":{"provider":"leshy","default_zone":"direct"},"startup":{"enabled":true},"endpoint_underlay_migration_pending":false}
JSON
    ;;
  profiles)
    if [[ "${2:-}" == "--json" ]]; then
      cat <<'JSON'
{"schema_version":1,"active":"office","profiles":[{"name":"office","active":true,"primary":{"interface":"amn0","endpoint_provider":"static","provider_args":""},"secondary":{"interface":"tun0","endpoint_provider":"command","provider_args":"/usr/local/libexec/provider"}}]}
JSON
    elif [[ "${2:-}" == "use" && "${3:-}" == "office" ]]; then
      exit 0
    else
      exit 21
    fi
    ;;
  endpoints)
    if [[ "${2:-}" == "--json" ]]; then
      cat <<'JSON'
{"schema_version":1,"roles":{"primary":{"interface":"amn0","provider":"static","provider_args":"","state":"ready","pending":false,"configured":["203.0.113.10"],"installed":["203.0.113.10"],"actions":{"rediscover":true,"invalidate":false}},"secondary":{"interface":"tun0","provider":"command","provider_args":"/usr/local/libexec/provider","state":"ready","pending":false,"configured":[],"installed":["198.51.100.40"],"actions":{"rediscover":true,"invalidate":false}}}}
JSON
    elif [[ "${2:-}" == "rediscover" && "${3:-}" == "primary" ]]; then
      exit 0
    elif [[ "${2:-}" == "invalidate" ]]; then
      printf 'generic invalidation unsupported\n' >&2
      exit 1
    else
      exit 22
    fi
    ;;
  dns)
    if [[ "${2:-}" == "--json" ]]; then
      cat <<'JSON'
{"schema_version":1,"provider":"leshy","interface":{"name":"leshy-dns0","state":"active"},"service":"running","listen":"127.0.0.1:53053"}
JSON
    elif [[ "${2:-}" == "enable" || "${2:-}" == "disable" ]]; then
      exit 0
    else
      exit 23
    fi
    ;;
  logs)
    [[ "${2:-}" == "--json" && "${3:-}" == "--lines" && "${4:-}" == "42" ]] || exit 24
    cat <<'JSON'
{"schema_version":1,"units":["leshy.service"],"limit":42,"entries":[{"MESSAGE":"hello","_SYSTEMD_UNIT":"leshy.service"}]}
JSON
    ;;
  start|stop|restart|enable|disable)
    exit 0
    ;;
  *) exit 25 ;;
esac
"#,
        )
    }

    #[tokio::test]
    async fn parses_json_contract_and_preserves_cli_mutation_commands() {
        let script = contract_cli();
        let log = PathBuf::from(format!("{}.log", script.display()));
        let backend = LinuxBackend {
            cli: script.clone().into_os_string(),
        };

        let status = backend.status().await.expect("status JSON");
        assert_eq!(status.schema_version, 1);
        assert_eq!(status.service, "running");
        assert_eq!(status.profiles.active.as_deref(), Some("office"));
        assert_eq!(status.interfaces.primary.name, "amn0");

        let profiles = backend.profiles().await.expect("profiles JSON");
        assert_eq!(profiles.schema_version, 1);
        assert_eq!(profiles.active.as_deref(), Some("office"));
        assert_eq!(profiles.profiles.len(), 1);
        assert_eq!(profiles.profiles[0].secondary.endpoint_provider, "command");

        let endpoints = backend.endpoints().await.expect("endpoints JSON");
        assert_eq!(endpoints.schema_version, 1);
        assert_eq!(endpoints.roles.primary.installed, ["203.0.113.10"]);
        assert_eq!(endpoints.roles.secondary.provider, "command");
        assert!(endpoints.roles.primary.actions.rediscover);
        assert!(!endpoints.roles.primary.actions.invalidate);

        let dns = backend.dns().await.expect("dns JSON");
        assert_eq!(dns.schema_version, 1);
        assert_eq!(dns.provider, "leshy");
        assert_eq!(dns.listen, "127.0.0.1:53053");

        let logs = backend.logs(42).await.expect("logs JSON");
        assert_eq!(logs.schema_version, 1);
        assert_eq!(logs.limit, 42);
        assert_eq!(logs.entries[0]["MESSAGE"], "hello");

        backend
            .service(ServiceAction::Start)
            .await
            .expect("start service");
        backend.use_profile("office").await.expect("use profile");
        backend.set_dns(false).await.expect("disable Leshy DNS");
        backend.set_startup(true).await.expect("enable startup");
        backend
            .rediscover_endpoints("primary")
            .await
            .expect("rediscover endpoints");

        let invalidate_error = backend
            .invalidate_endpoint_cache("primary")
            .await
            .expect_err("generic invalidation must stay unsupported");
        assert!(invalidate_error
            .to_string()
            .contains("generic invalidation unsupported"));

        let calls = fs::read_to_string(&log).expect("read fake kk calls");
        assert_eq!(
            calls.lines().collect::<Vec<_>>(),
            vec![
                "status --json",
                "profiles --json",
                "endpoints --json",
                "dns --json",
                "logs --json --lines 42",
                "start",
                "profiles use office",
                "dns disable",
                "enable",
                "endpoints rediscover primary",
                "endpoints invalidate primary",
            ]
        );

        let _ = fs::remove_file(log);
        let _ = fs::remove_file(script);
    }

    #[tokio::test]
    async fn rejects_invalid_json_from_cli() {
        let script = fake_cli(
            r#"#!/usr/bin/env bash
printf '{not-json}\n'
"#,
        );
        let backend = LinuxBackend {
            cli: script.clone().into_os_string(),
        };

        let error = backend.status().await.expect_err("invalid JSON must fail");
        assert!(error.to_string().contains("invalid JSON from kk status --json"));

        let _ = fs::remove_file(script);
    }
}
