//! Compose status view (M2+M5): the project's services in a table,
//! refreshed manually with `r` and automatically on an interval. Enter
//! opens the log view for the selected service; `R`/`s`/`S` restart, stop,
//! or start it; `x` opens the predefined-scripts menu. Actions and scripts
//! stream their output into the same follow view as logs.

use std::time::{Duration, Instant};

use anyhow::Result;
use crossterm::event::{KeyCode, KeyEvent};
use linqode_compose::{Service, ServiceAction, action_command, logs_command};
use ratatui::Frame;
use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::style::{Color, Modifier, Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Clear, Paragraph, Row, Table, TableState};

use crate::app::AppInfo;

const AUTO_REFRESH: Duration = Duration::from_secs(5);
/// How many lines of history `docker compose logs` starts with.
const LOG_TAIL: u32 = 200;

type RefreshFn<'a> = &'a mut dyn FnMut() -> Result<Vec<Service>>;

/// What the app loop should do after a key press.
pub(crate) enum StatusAction {
    None,
    Quit,
    /// Run `command` remotely and stream its output in a follow view.
    Follow { title: String, command: String },
}

pub(crate) struct StatusView {
    services: Vec<Service>,
    selected: usize,
    /// Last refresh failure; the previous service list stays on screen.
    error: Option<String>,
    last_refresh: Instant,
    /// Selected index in the scripts popup; `Some` routes keys to it.
    script_menu: Option<usize>,
}

impl StatusView {
    pub fn new() -> Self {
        Self {
            services: Vec::new(),
            selected: 0,
            error: None,
            last_refresh: Instant::now(),
            script_menu: None,
        }
    }

    pub fn refresh(&mut self, refresh: RefreshFn) {
        match refresh() {
            Ok(services) => {
                // Keep the cursor on the same service across refreshes.
                let selected = self.services.get(self.selected).map(|s| s.name.clone());
                self.selected = selected
                    .and_then(|name| services.iter().position(|s| s.name == name))
                    .unwrap_or_else(|| self.selected.min(services.len().saturating_sub(1)));
                self.services = services;
                self.error = None;
            }
            Err(err) => self.error = Some(format!("{err:#}")),
        }
        self.last_refresh = Instant::now();
    }

    pub fn maybe_auto_refresh(&mut self, refresh: RefreshFn) {
        if self.last_refresh.elapsed() >= AUTO_REFRESH {
            self.refresh(refresh);
        }
    }

    pub fn set_error(&mut self, error: String) {
        self.error = Some(error);
    }

    pub fn handle_key(&mut self, key: KeyEvent, info: &AppInfo, refresh: RefreshFn) -> StatusAction {
        if let Some(selected) = &mut self.script_menu {
            match key.code {
                KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('x') => self.script_menu = None,
                KeyCode::Char('j') | KeyCode::Down => {
                    *selected = (*selected + 1).min(info.scripts.len().saturating_sub(1));
                }
                KeyCode::Char('k') | KeyCode::Up => *selected = selected.saturating_sub(1),
                KeyCode::Enter => {
                    if let Some((name, command)) = info.scripts.get(*selected).cloned() {
                        self.script_menu = None;
                        return StatusAction::Follow {
                            title: format!("script: {name}"),
                            command,
                        };
                    }
                }
                _ => {}
            }
            return StatusAction::None;
        }

        match key.code {
            KeyCode::Char('q') | KeyCode::Esc => return StatusAction::Quit,
            KeyCode::Char('j') | KeyCode::Down => self.select(1),
            KeyCode::Char('k') | KeyCode::Up => self.select(-1),
            KeyCode::Char('g') | KeyCode::Home => self.selected = 0,
            KeyCode::Char('G') | KeyCode::End => {
                self.selected = self.services.len().saturating_sub(1);
            }
            KeyCode::Char('r') => self.refresh(refresh),
            KeyCode::Enter | KeyCode::Char('l') => {
                if let Some(service) = self.services.get(self.selected) {
                    return StatusAction::Follow {
                        title: format!("logs: {}", service.service),
                        command: logs_command(
                            info.compose_dir.as_deref(),
                            &service.service,
                            LOG_TAIL,
                        ),
                    };
                }
            }
            KeyCode::Char('R') => return self.action(info, ServiceAction::Restart),
            KeyCode::Char('s') => return self.action(info, ServiceAction::Stop),
            KeyCode::Char('S') => return self.action(info, ServiceAction::Start),
            KeyCode::Char('x') => {
                if info.scripts.is_empty() {
                    self.error = Some("no scripts configured for this host".to_string());
                } else {
                    self.script_menu = Some(0);
                }
            }
            _ => {}
        }
        StatusAction::None
    }

    /// Runs a compose lifecycle action on the selected service.
    fn action(&self, info: &AppInfo, action: ServiceAction) -> StatusAction {
        let Some(service) = self.services.get(self.selected) else {
            return StatusAction::None;
        };
        StatusAction::Follow {
            title: format!("{}: {}", action.verb(), service.service),
            command: action_command(info.compose_dir.as_deref(), action, &service.service),
        }
    }

    fn select(&mut self, delta: i64) {
        if self.services.is_empty() {
            return;
        }
        let last = self.services.len() as i64 - 1;
        self.selected = (self.selected as i64 + delta).clamp(0, last) as usize;
    }

    pub fn draw(&self, frame: &mut Frame, info: &AppInfo) {
        let [header_area, table_area, footer_area] = Layout::vertical([
            Constraint::Length(1),
            Constraint::Min(1),
            Constraint::Length(1),
        ])
        .areas(frame.area());

        let mut header = vec![
            Span::styled(" linqode ", Style::default().add_modifier(Modifier::BOLD)),
            Span::raw(&info.target),
        ];
        if let Some(dir) = &info.compose_dir {
            header.push(Span::raw("  "));
            header.push(Span::styled(dir, Style::default().fg(Color::Cyan)));
        }
        frame.render_widget(Paragraph::new(Line::from(header)), header_area);

        let block = Block::bordered();
        if self.services.is_empty() {
            let message = match &self.error {
                Some(_) => "(no data — see error below)",
                None => "(no services in this compose project)",
            };
            frame.render_widget(
                Paragraph::new(message.dim()).block(block).centered(),
                table_area,
            );
        } else {
            let rows = self.services.iter().map(|service| {
                Row::new(vec![
                    Line::raw(service.service.as_str()),
                    Line::styled(service.state.as_str(), state_style(&service.state)),
                    health_line(&service.health),
                    Line::raw(service.ports_summary()),
                    Line::raw(service.status.as_str()),
                ])
            });
            let table = Table::new(
                rows,
                [
                    Constraint::Min(12),    // service
                    Constraint::Length(11), // state
                    Constraint::Length(9),  // health
                    Constraint::Min(14),    // ports
                    Constraint::Min(20),    // status
                ],
            )
            .header(Row::new(["SERVICE", "STATE", "HEALTH", "PORTS", "STATUS"]).dim())
            .row_highlight_style(Style::default().add_modifier(Modifier::REVERSED))
            .block(block);
            let mut state = TableState::default().with_selected(self.selected);
            frame.render_stateful_widget(table, table_area, &mut state);
        }

        let footer = if self.script_menu.is_some() {
            Line::from(vec![Span::styled(
                " j/k select · enter run · esc cancel",
                Style::default().add_modifier(Modifier::DIM),
            )])
        } else {
            match &self.error {
                Some(error) => Line::from(vec![
                    Span::raw(" "),
                    Span::styled(error.replace('\n', " · "), Style::default().fg(Color::Red)),
                ]),
                None => Line::from(vec![
                    Span::raw(format!(" {} services", self.services.len())),
                    Span::styled(
                        "  ·  enter logs · R restart · s stop · S start · x scripts \
                         · r refresh · q quit",
                        Style::default().add_modifier(Modifier::DIM),
                    ),
                ]),
            }
        };
        frame.render_widget(Paragraph::new(footer), footer_area);

        if let Some(selected) = self.script_menu {
            self.draw_scripts_menu(frame, info, table_area, selected);
        }
    }

    fn draw_scripts_menu(&self, frame: &mut Frame, info: &AppInfo, area: Rect, selected: usize) {
        let name_width = info
            .scripts
            .iter()
            .map(|(name, _)| name.len())
            .max()
            .unwrap_or(0);
        let lines: Vec<Line> = info
            .scripts
            .iter()
            .enumerate()
            .map(|(i, (name, command))| {
                let line = Line::from(vec![
                    Span::styled(
                        format!(" {name:<name_width$}  "),
                        Style::default().add_modifier(Modifier::BOLD),
                    ),
                    Span::styled(command.as_str(), Style::default().add_modifier(Modifier::DIM)),
                    Span::raw(" "),
                ]);
                if i == selected {
                    line.style(Style::default().add_modifier(Modifier::REVERSED))
                } else {
                    line
                }
            })
            .collect();

        let width = lines
            .iter()
            .map(Line::width)
            .max()
            .unwrap_or(0)
            .clamp(20, area.width.saturating_sub(4) as usize) as u16
            + 2;
        let height = (info.scripts.len() as u16 + 2).min(area.height);
        let popup = Rect {
            x: area.x + area.width.saturating_sub(width) / 2,
            y: area.y + area.height.saturating_sub(height) / 2,
            width: width.min(area.width),
            height,
        };
        frame.render_widget(Clear, popup);
        frame.render_widget(
            Paragraph::new(lines).block(Block::bordered().title(" scripts ")),
            popup,
        );
    }
}

fn state_style(state: &str) -> Style {
    match state {
        "running" => Style::default().fg(Color::Green),
        "restarting" | "paused" | "created" => Style::default().fg(Color::Yellow),
        "exited" | "dead" => Style::default().fg(Color::Red),
        _ => Style::default(),
    }
}

fn health_line(health: &str) -> Line<'_> {
    match health {
        "" => Line::styled("-", Style::default().add_modifier(Modifier::DIM)),
        "healthy" => Line::styled(health, Style::default().fg(Color::Green)),
        "starting" => Line::styled(health, Style::default().fg(Color::Yellow)),
        "unhealthy" => Line::styled(health, Style::default().fg(Color::Red)),
        other => Line::raw(other),
    }
}
