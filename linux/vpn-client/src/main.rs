use kikimora_vpn::backends::build_backend;
use kikimora_vpn::config::ClientConfig;
use kikimora_vpn::import::{import_share_link, render_imported_toml, write_imported_config};
use kikimora_vpn::runtime::run_runtime;
use kikimora_vpn::tun::LinuxTun;
use std::error::Error;
use std::io::Read;
use std::path::PathBuf;
use tokio::sync::watch;

enum Command {
    Run {
        config: PathBuf,
        check: bool,
    },
    Import {
        link: Option<String>,
        name: String,
        interface: String,
        output: PathBuf,
        force: bool,
    },
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    match parse_args()? {
        Command::Run { config, check } => run_command(config, check).await,
        Command::Import {
            link,
            name,
            interface,
            output,
            force,
        } => import_command(link, name, interface, output, force),
    }
}

async fn run_command(config_path: PathBuf, check: bool) -> Result<(), Box<dyn Error>> {
    let config = ClientConfig::load(&config_path)?;

    if check {
        println!(
            "configuration OK: name={} protocol={} interface={}",
            config.name,
            config.protocol.as_str(),
            config.interface
        );
        return Ok(());
    }

    let backend = build_backend(&config)?;
    let tun = LinuxTun::create(&config.interface, &config.address, config.mtu)?;
    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    spawn_signal_task(shutdown_tx)?;

    run_runtime(config, Box::new(tun), backend, shutdown_rx).await?;
    Ok(())
}

fn import_command(
    link: Option<String>,
    name: String,
    interface: String,
    output: PathBuf,
    force: bool,
) -> Result<(), Box<dyn Error>> {
    let link = match link {
        Some(link) => link,
        None => {
            let mut input = String::new();
            std::io::stdin().read_to_string(&mut input)?;
            let input = input.trim();
            if input.is_empty() {
                return Err("import link is empty".into());
            }
            input.to_string()
        }
    };

    let config = import_share_link(&link, &name, &interface)?;
    let text = render_imported_toml(&config)?;
    write_imported_config(&output, &text, force)?;
    println!(
        "imported client: name={} protocol={} interface={} output={}",
        config.name,
        config.protocol.as_str(),
        config.interface,
        output.display()
    );
    Ok(())
}

fn parse_args() -> Result<Command, Box<dyn Error>> {
    let mut args = std::env::args().skip(1).peekable();
    if matches!(args.peek().map(String::as_str), Some("import")) {
        args.next();
        return parse_import_args(args.collect());
    }

    let mut config = None;
    let mut check = false;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--config" => {
                config = Some(PathBuf::from(
                    args.next().ok_or("--config requires a path")?,
                ));
            }
            "--check" => check = true,
            "-h" | "--help" => {
                print_help();
                std::process::exit(0);
            }
            other => return Err(format!("unexpected argument: {other}").into()),
        }
    }

    Ok(Command::Run {
        config: config.ok_or("usage: kikimora-vpn --config PATH [--check]")?,
        check,
    })
}

fn parse_import_args(args: Vec<String>) -> Result<Command, Box<dyn Error>> {
    let mut link = None;
    let mut name = None;
    let mut interface = None;
    let mut output = None;
    let mut force = false;
    let mut args = args.into_iter();

    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--name" => name = Some(args.next().ok_or("--name requires a value")?),
            "--interface" => interface = Some(args.next().ok_or("--interface requires a value")?),
            "--output" => {
                output = Some(PathBuf::from(
                    args.next().ok_or("--output requires a path")?,
                ))
            }
            "--force" => force = true,
            "-h" | "--help" => {
                print_import_help();
                std::process::exit(0);
            }
            other if other.starts_with('-') => {
                return Err(format!("unexpected import option: {other}").into())
            }
            other => {
                if link.replace(other.to_string()).is_some() {
                    return Err("import accepts at most one positional share link".into());
                }
            }
        }
    }

    Ok(Command::Import {
        link,
        name: name.ok_or("import requires --name NAME")?,
        interface: interface.ok_or("import requires --interface IFACE")?,
        output: output.ok_or("import requires --output PATH")?,
        force,
    })
}

fn spawn_signal_task(shutdown: watch::Sender<bool>) -> Result<(), Box<dyn Error>> {
    #[cfg(unix)]
    {
        use tokio::signal::unix::{signal, SignalKind};
        let mut terminate = signal(SignalKind::terminate())?;
        tokio::spawn(async move {
            tokio::select! {
                _ = tokio::signal::ctrl_c() => {}
                _ = terminate.recv() => {}
            }
            let _ = shutdown.send(true);
        });
    }

    #[cfg(not(unix))]
    {
        tokio::spawn(async move {
            let _ = tokio::signal::ctrl_c().await;
            let _ = shutdown.send(true);
        });
    }

    Ok(())
}

fn print_help() {
    println!(
        "kikimora-vpn\n\nUSAGE:\n  kikimora-vpn --config PATH [--check]\n  kikimora-vpn import [SHARE_LINK] --name NAME --interface IFACE --output PATH [--force]\n\nCOMMANDS:\n  import         Convert wg://, awg://, amneziawg://, wireguard:// or VLESS+REALITY vless:// share links into a normalized client TOML. If SHARE_LINK is omitted it is read from stdin.\n\nOPTIONS:\n  --config PATH   Instance configuration\n  --check         Parse and validate configuration without creating a TUN\n"
    );
}

fn print_import_help() {
    println!(
        "kikimora-vpn import\n\nUSAGE:\n  kikimora-vpn import [SHARE_LINK] --name NAME --interface IFACE --output PATH [--force]\n\nFor WG/AWG links prefer stdin so the embedded private key does not enter shell history or the process argument list:\n  printf '%s\\n' \"$VPN_LINK\" | kikimora-vpn import --name awg-main --interface kk-awg0 --output /etc/kikimora/vpn/clients/awg-main.toml\n"
    );
}
