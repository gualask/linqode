//! Main application loop: dispatches ticks, drawing, and keys to the active
//! view (compose status or log follow).

use std::time::Duration;

use anyhow::Result;
use crossterm::event::{self, Event, KeyCode, KeyEventKind, KeyModifiers};
use linqode_compose::Service;
use linqode_logs::LogFeed;

use crate::logs::{LogView, LogsAction};
use crate::status::{StatusAction, StatusView};

/// How often the loop wakes up without input, to drain log feeds and check
/// the auto-refresh timer.
const TICK: Duration = Duration::from_millis(150);

/// Static context for the session: header fields plus what the config
/// makes runnable.
pub struct AppInfo {
    /// `user@host` the session is connected to.
    pub target: String,
    /// Remote directory of the compose project, if configured.
    pub compose_dir: Option<String>,
    /// Predefined scripts from the config, `(name, command)` sorted by name.
    pub scripts: Vec<(String, String)>,
}

/// Runs the app until the user quits. `refresh` fetches and parses
/// `docker compose ps` output; `exec` starts a remote command (log follow,
/// service action, script) streaming into a feed. Both block briefly on
/// the SSH round-trip.
pub fn run_app(
    info: &AppInfo,
    refresh: &mut dyn FnMut() -> Result<Vec<Service>>,
    exec: &mut dyn FnMut(&str) -> Result<LogFeed>,
) -> Result<()> {
    let mut status = StatusView::new();
    status.refresh(refresh);
    let mut logs: Option<LogView> = None;

    let mut terminal = ratatui::init();
    let result = (|| {
        loop {
            match &mut logs {
                Some(view) => view.tick(),
                None => status.maybe_auto_refresh(refresh),
            }
            terminal.draw(|frame| match &mut logs {
                Some(view) => view.draw(frame, info),
                None => status.draw(frame, info),
            })?;

            if !event::poll(TICK)? {
                continue;
            }
            let Event::Key(key) = event::read()? else {
                continue;
            };
            if key.kind != KeyEventKind::Press {
                continue;
            }
            if key.code == KeyCode::Char('c') && key.modifiers.contains(KeyModifiers::CONTROL) {
                return Ok(());
            }

            match &mut logs {
                Some(view) => {
                    if view.handle_key(key) == LogsAction::Close {
                        logs = None; // dropping the view cancels the remote follower
                        status.refresh(refresh);
                    }
                }
                None => match status.handle_key(key, info, refresh) {
                    StatusAction::Quit => return Ok(()),
                    StatusAction::Follow { title, command } => match exec(&command) {
                        Ok(feed) => logs = Some(LogView::new(title, feed)),
                        Err(err) => status.set_error(format!("{err:#}")),
                    },
                    StatusAction::None => {}
                },
            }
        }
    })();
    ratatui::restore();
    result
}
