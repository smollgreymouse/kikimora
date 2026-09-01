use kikimora_vpn::backends::build_backend;
use kikimora_vpn::config::ClientConfig;
use kikimora_vpn::runtime::run_runtime;
use kikimora_vpn::tun::LinuxTun;
use std::error::Error;
use std::path::PathBuf;
use tokio::sync::watch;

struct Args {
    config: PathBuf,
    check: bool,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let args = parse_args()?;
    let config = ClientConfig::load(&args.config)?;

    if args.check {
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

fn parse_args() -> Result<Args, Box<dyn Error>> {
    let mut config = None;
    let mut check = false;
    let mut args = std::env::args().skip(1);

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

    Ok(Args {
        config: config.ok_or("usage: kikimora-vpn --config PATH [--check]")?,
        check,
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
        "kikimora-vpn\n\nUSAGE:\n  kikimora-vpn --config PATH [--check]\n\nOPTIONS:\n  --config PATH   Instance configuration\n  --check         Parse and validate configuration without creating a TUN\n"
    );
}
