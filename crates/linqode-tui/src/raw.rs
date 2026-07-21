//! Raw exec screen (M1): show the output of a one-shot remote command,
//! scrollable, with rerun. Used for `linqode --exec CMD`.

use anyhow::Result;
use crossterm::event::{self, Event, KeyCode, KeyEventKind, KeyModifiers};
use ratatui::Frame;
use ratatui::layout::{Constraint, Layout};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span, Text};
use ratatui::widgets::{Block, Paragraph};
use linqode_ssh::ExecOutput;

/// Static context shown in the header.
pub struct SessionInfo {
    /// `user@host` the session is connected to.
    pub target: String,
    /// The remote command being shown.
    pub command: String,
}

struct App {
    output: ExecOutput,
    /// First visible output line.
    scroll: u16,
    /// Height of the output viewport at last render, for clamping.
    viewport_height: u16,
}

/// Runs the raw exec screen until the user quits. `rerun` executes the
/// command again (blocking) when the user presses `r`.
pub fn run_raw(
    info: &SessionInfo,
    initial: ExecOutput,
    rerun: &mut dyn FnMut() -> Result<ExecOutput, linqode_ssh::Error>,
) -> Result<()> {
    let mut terminal = ratatui::init();
    let mut app = App {
        output: initial,
        scroll: 0,
        viewport_height: 0,
    };

    let result = loop {
        if let Err(err) = terminal.draw(|frame| draw(frame, info, &mut app)) {
            break Err(err.into());
        }
        match event::read() {
            Ok(Event::Key(key)) if key.kind == KeyEventKind::Press => match key.code {
                KeyCode::Char('q') | KeyCode::Esc => break Ok(()),
                KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                    break Ok(());
                }
                KeyCode::Char('j') | KeyCode::Down => app.scroll_by(1),
                KeyCode::Char('k') | KeyCode::Up => app.scroll_by(-1),
                KeyCode::PageDown => app.scroll_by(i32::from(app.viewport_height)),
                KeyCode::PageUp => app.scroll_by(-i32::from(app.viewport_height)),
                KeyCode::Char('g') | KeyCode::Home => app.scroll = 0,
                KeyCode::Char('G') | KeyCode::End => app.scroll = u16::MAX, // clamped on draw
                KeyCode::Char('r') => match rerun() {
                    Ok(output) => {
                        app.output = output;
                        app.scroll = 0;
                    }
                    Err(err) => {
                        app.output.stderr = format!("rerun failed: {err}\n").into_bytes();
                        app.output.exit_code = None;
                    }
                },
                _ => {}
            },
            Ok(_) => {}
            Err(err) => break Err(err.into()),
        }
    };

    ratatui::restore();
    result
}

impl App {
    fn scroll_by(&mut self, delta: i32) {
        self.scroll = self.scroll.saturating_add_signed(delta.clamp(-32768, 32767) as i16);
    }

    fn lines(&self) -> Vec<Line<'static>> {
        let mut lines: Vec<Line> = Vec::new();
        for chunk in String::from_utf8_lossy(&self.output.stdout).lines() {
            lines.push(Line::raw(chunk.to_string()));
        }
        for chunk in String::from_utf8_lossy(&self.output.stderr).lines() {
            lines.push(Line::styled(
                chunk.to_string(),
                Style::default().fg(Color::Red),
            ));
        }
        if lines.is_empty() {
            lines.push(Line::styled(
                "(no output)",
                Style::default().add_modifier(Modifier::DIM),
            ));
        }
        lines
    }
}

fn draw(frame: &mut Frame, info: &SessionInfo, app: &mut App) {
    let [header_area, output_area, footer_area] = Layout::vertical([
        Constraint::Length(1),
        Constraint::Min(1),
        Constraint::Length(1),
    ])
    .areas(frame.area());

    let header = Line::from(vec![
        Span::styled(" linqode ", Style::default().add_modifier(Modifier::BOLD)),
        Span::raw(&info.target),
        Span::raw("  $ "),
        Span::styled(&info.command, Style::default().fg(Color::Cyan)),
    ]);
    frame.render_widget(Paragraph::new(header), header_area);

    let lines = app.lines();
    let block = Block::bordered();
    app.viewport_height = block.inner(output_area).height;
    let max_scroll = (lines.len() as u16).saturating_sub(app.viewport_height);
    app.scroll = app.scroll.min(max_scroll);
    frame.render_widget(
        Paragraph::new(Text::from(lines))
            .block(block)
            .scroll((app.scroll, 0)),
        output_area,
    );

    let status = match app.output.exit_code {
        Some(0) => Span::styled("exit 0", Style::default().fg(Color::Green)),
        Some(code) => Span::styled(format!("exit {code}"), Style::default().fg(Color::Red)),
        None => Span::styled("no exit status", Style::default().add_modifier(Modifier::DIM)),
    };
    let footer = Line::from(vec![
        Span::raw(" "),
        status,
        Span::styled(
            "  ·  j/k scroll · g/G top/bottom · r rerun · q quit",
            Style::default().add_modifier(Modifier::DIM),
        ),
    ]);
    frame.render_widget(Paragraph::new(footer), footer_area);
}
