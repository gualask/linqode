mod config;
mod prompt;

use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{Result, bail};
use clap::Parser;
use linqode_compose::Service;
use linqode_logs::{LineAssembler, LogEvent, LogFeed};
use linqode_ssh::{ExecEvent, ExecOutput, ExecStream, Remote, Session, Target};
use linqode_tui::{AppInfo, SessionInfo};

use crate::config::Config;
use crate::prompt::TerminalPrompter;

/// How many lines of history `docker compose logs` starts with.
const LOG_TAIL: u32 = 200;

/// SSH TUI for Docker Compose management.
#[derive(Parser)]
#[command(name = "linqode", version)]
struct Cli {
    /// Host to connect to: a name from the config file or an inline
    /// [user@]host[:port]. Optional when the config has exactly one host.
    host: Option<String>,

    /// Config file path (default: ~/.config/linqode/config.toml)
    #[arg(long, value_name = "FILE")]
    config: Option<PathBuf>,

    /// Run this command and show its raw output instead of the compose
    /// status view
    #[arg(short = 'e', long = "exec", value_name = "CMD")]
    command: Option<String>,
}

fn main() -> Result<()> {
    let cli = Cli::parse();
    let config = Config::load(cli.config.as_deref())?;
    let selection = config.select(cli.host.as_deref())?;
    let target = Target::resolve(&selection.spec)?;

    // block_in_place in the connect path requires the multi-thread runtime.
    let runtime = tokio::runtime::Runtime::new()?;

    eprintln!(
        "Connecting to {}@{}:{} ...",
        target.user, target.display_host, target.port
    );
    let session = runtime.block_on(Session::connect(&target, Arc::new(TerminalPrompter)))?;
    let target_label = format!("{}@{}", target.user, target.display_host);

    let result = match cli.command {
        Some(command) => {
            let initial = runtime.block_on(session.exec(&command))?;
            let info = SessionInfo {
                target: target_label,
                command: command.clone(),
            };
            linqode_tui::run_raw(&info, initial, &mut || {
                runtime.block_on(session.exec(&command))
            })
        }
        None => {
            let ps_command = linqode_compose::ps_command(selection.compose_dir.as_deref());
            let compose_dir = selection.compose_dir.clone();
            let info = AppInfo {
                target: target_label,
                compose_dir: selection.compose_dir.clone(),
            };
            linqode_tui::run_app(
                &info,
                &mut || fetch_services(&runtime, &session, &ps_command),
                &mut |service| {
                    let command =
                        linqode_compose::logs_command(compose_dir.as_deref(), service, LOG_TAIL);
                    start_log_feed(&runtime, &session, &command)
                },
            )
        }
    };

    runtime.block_on(session.close());
    result
}

/// Runs `docker compose ps` remotely and parses its output.
fn fetch_services(
    runtime: &tokio::runtime::Runtime,
    session: &Session,
    command: &str,
) -> Result<Vec<Service>> {
    let output = runtime.block_on(session.exec(command))?;
    if output.exit_code != Some(0) {
        bail!("{}", exec_failure(&output));
    }
    Ok(linqode_compose::parse_ps(&output.stdout)?)
}

/// Starts a remote log follower and pumps its byte chunks into complete
/// lines for the TUI. The pump runs on the tokio runtime; the TUI polls the
/// returned feed from its synchronous event loop.
fn start_log_feed(
    runtime: &tokio::runtime::Runtime,
    session: &Session,
    command: &str,
) -> Result<LogFeed> {
    let ExecStream { mut events, cancel } = runtime.block_on(session.exec_stream(command))?;

    let (tx, rx) = std::sync::mpsc::channel();
    runtime.spawn(async move {
        let mut stdout = LineAssembler::default();
        let mut stderr = LineAssembler::default();
        let mut exit_code = None;
        while let Some(event) = events.recv().await {
            let sent = match event {
                ExecEvent::Stdout(bytes) => stdout
                    .push(&bytes)
                    .into_iter()
                    .try_for_each(|line| tx.send(LogEvent::Line(line))),
                ExecEvent::Stderr(bytes) => stderr
                    .push(&bytes)
                    .into_iter()
                    .try_for_each(|line| tx.send(LogEvent::Stderr(line))),
                ExecEvent::Exit(code) => {
                    exit_code = Some(code);
                    Ok(())
                }
            };
            if sent.is_err() {
                return; // the TUI dropped the feed
            }
        }
        if let Some(line) = stdout.finish() {
            let _ = tx.send(LogEvent::Line(line));
        }
        if let Some(line) = stderr.finish() {
            let _ = tx.send(LogEvent::Stderr(line));
        }
        let _ = tx.send(LogEvent::Ended { exit_code });
    });

    Ok(LogFeed::new(rx, move || cancel.cancel()))
}

/// A one-line reason for a failed remote command: the last stderr line when
/// there is one (compose puts the actual error there), the exit code otherwise.
fn exec_failure(output: &ExecOutput) -> String {
    let stderr = String::from_utf8_lossy(&output.stderr);
    match stderr.lines().rev().find(|line| !line.trim().is_empty()) {
        Some(line) => line.trim().to_string(),
        None => match output.exit_code {
            Some(code) => format!("remote command failed with exit code {code}"),
            None => "remote command failed without an exit status".to_string(),
        },
    }
}
