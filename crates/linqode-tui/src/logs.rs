//! Log follow view (M3+M4): tail of `docker compose logs -f` for one
//! service, with scrollback, search, and structured-log analysis — JSONL
//! detection, `key=value` field filters, and a live stats panel (counts by
//! level, top values of a field).

use crossterm::event::{KeyCode, KeyEvent};
use linqode_logs::{Filter, LogEvent, LogFeed, LogLine, LogStore, find_ascii_ci};
use ratatui::Frame;
use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::style::{Color, Modifier, Style, Stylize};
use ratatui::text::{Line, Span, Text};
use ratatui::widgets::{Block, Paragraph};

use crate::app::AppInfo;

/// Scrollback kept in memory per followed service.
const CAPACITY: usize = 10_000;
/// Upper bound of feed events applied per tick, so a log burst cannot
/// starve input handling.
const MAX_EVENTS_PER_TICK: usize = 5_000;
/// Width of the stats side panel, and how many top values it lists.
const STATS_WIDTH: u16 = 28;
const TOP_VALUES: usize = 8;

/// What the app loop should do after a key press.
#[derive(PartialEq, Eq)]
pub(crate) enum LogsAction {
    None,
    /// Back to the status view; dropping the view cancels the follower.
    Close,
}

/// Which prompt the footer input line is collecting.
enum InputMode {
    /// `/` — text search over raw lines.
    Search,
    /// `f` — field filter expression (`level=error app!=web`).
    Filter,
    /// `t` — field whose top values the stats panel counts.
    TopField,
}

pub(crate) struct LogView {
    service: String,
    feed: LogFeed,
    store: LogStore,
    /// Index of the top visible line; recomputed on draw when following.
    scroll: usize,
    follow: bool,
    /// Viewport height at last render, for paging and clamping.
    viewport: u16,
    /// `Some` once the stream ended remotely, with its exit code.
    ended: Option<Option<u32>>,
    /// Committed search query.
    query: Option<String>,
    /// Line index of the current match, anchor for n/N.
    match_line: Option<usize>,
    /// Footer input being typed; `Some` routes keys to the input field.
    input: Option<(InputMode, String)>,
    /// Structured rendering: `None` follows JSONL auto-detection, `Some`
    /// is a manual override (`s`).
    structured: Option<bool>,
    show_stats: bool,
    /// One-line feedback, e.g. "no match".
    notice: Option<String>,
    /// Last stderr line from the remote command (compose diagnostics).
    stderr_notice: Option<String>,
}

impl LogView {
    pub fn new(service: String, feed: LogFeed) -> Self {
        Self {
            service,
            feed,
            store: LogStore::new(CAPACITY),
            scroll: 0,
            follow: true,
            viewport: 0,
            ended: None,
            query: None,
            match_line: None,
            input: None,
            structured: None,
            show_stats: false,
            notice: None,
            stderr_notice: None,
        }
    }

    /// Drains pending feed events into the store.
    pub fn tick(&mut self) {
        for _ in 0..MAX_EVENTS_PER_TICK {
            match self.feed.try_next() {
                Some(LogEvent::Line(line)) => {
                    let dropped = self.store.push(line);
                    if dropped > 0 {
                        self.scroll = self.scroll.saturating_sub(dropped);
                        self.match_line = self.match_line.and_then(|i| i.checked_sub(dropped));
                    }
                }
                Some(LogEvent::Stderr(line)) => self.stderr_notice = Some(line),
                Some(LogEvent::Ended { exit_code }) => self.ended = Some(exit_code),
                None => break,
            }
        }
    }

    pub fn handle_key(&mut self, key: KeyEvent) -> LogsAction {
        if let Some((_, text)) = &mut self.input {
            match key.code {
                KeyCode::Esc => self.input = None,
                KeyCode::Enter => {
                    let (mode, text) = self.input.take().expect("input mode");
                    self.commit_input(&mode, text);
                }
                KeyCode::Backspace => {
                    text.pop();
                }
                KeyCode::Char(c) => text.push(c),
                _ => {}
            }
            return LogsAction::None;
        }

        match key.code {
            KeyCode::Char('q') | KeyCode::Esc => return LogsAction::Close,
            KeyCode::Char('j') | KeyCode::Down => self.scroll_by(1),
            KeyCode::Char('k') | KeyCode::Up => self.scroll_by(-1),
            KeyCode::PageDown => self.scroll_by(i64::from(self.viewport)),
            KeyCode::PageUp => self.scroll_by(-i64::from(self.viewport)),
            KeyCode::Char('g') | KeyCode::Home => {
                self.follow = false;
                self.scroll = 0;
            }
            KeyCode::Char('G') | KeyCode::End => self.follow = true,
            KeyCode::Char('/') => self.input = Some((InputMode::Search, String::new())),
            KeyCode::Char('n') => self.next_match(true),
            KeyCode::Char('N') => self.next_match(false),
            KeyCode::Char('f') => {
                let current = self.store.filter().map(Filter::expr).unwrap_or_default();
                self.input = Some((InputMode::Filter, current.to_string()));
            }
            KeyCode::Char('s') => {
                let effective = self.structured_rendering();
                self.structured = Some(!effective);
            }
            KeyCode::Char('a') => self.show_stats = !self.show_stats,
            KeyCode::Char('t') => {
                let current = self.store.stats().field().unwrap_or_default();
                self.input = Some((InputMode::TopField, current.to_string()));
            }
            _ => {}
        }
        LogsAction::None
    }

    fn commit_input(&mut self, mode: &InputMode, text: String) {
        self.notice = None;
        match mode {
            InputMode::Search => {
                self.match_line = None;
                self.query = (!text.is_empty()).then_some(text);
                if self.query.is_some() {
                    self.jump(true, self.scroll);
                }
            }
            InputMode::Filter => match Filter::parse(&text) {
                Ok(filter) => {
                    self.store.set_filter(filter);
                    self.match_line = None;
                    self.follow = true;
                }
                Err(reason) => self.notice = Some(format!("bad filter: {reason}")),
            },
            InputMode::TopField => {
                self.store.set_top_field((!text.is_empty()).then_some(text));
                self.show_stats = true;
            }
        }
    }

    /// Whether lines render in structured (parsed JSONL) form.
    fn structured_rendering(&self) -> bool {
        self.structured.unwrap_or_else(|| self.store.looks_structured())
    }

    fn max_scroll(&self) -> usize {
        self.store.len().saturating_sub(self.viewport as usize)
    }

    fn scroll_by(&mut self, delta: i64) {
        let max = self.max_scroll();
        self.scroll = (self.scroll as i64 + delta).clamp(0, max as i64) as usize;
        // Scrolling up leaves follow mode; hitting bottom re-enters it.
        self.follow = delta > 0 && self.scroll >= max;
    }

    /// Searches from `from` and scrolls to the match.
    fn jump(&mut self, forward: bool, from: usize) {
        let Some(query) = &self.query else {
            self.notice = Some("no search query (use /)".to_string());
            return;
        };
        let found = if forward {
            self.store.search_next(query, from)
        } else {
            self.store.search_prev(query, from)
        };
        match found {
            Some(index) => {
                self.match_line = Some(index);
                self.follow = false;
                self.scroll = index.saturating_sub(usize::from(self.viewport) / 3);
                self.notice = None;
            }
            None => self.notice = Some(format!("no match for `{query}`")),
        }
    }

    fn next_match(&mut self, forward: bool) {
        let len = self.store.len();
        if len == 0 {
            return;
        }
        let from = match self.match_line {
            Some(current) if forward => (current + 1) % len,
            Some(current) => current.checked_sub(1).unwrap_or(len - 1),
            None => self.scroll,
        };
        self.jump(forward, from);
    }

    pub fn draw(&mut self, frame: &mut Frame, info: &AppInfo) {
        let [header_area, body_area, footer_area] = Layout::vertical([
            Constraint::Length(1),
            Constraint::Min(1),
            Constraint::Length(1),
        ])
        .areas(frame.area());

        let mut header = vec![
            Span::styled(" linqode ", Style::default().add_modifier(Modifier::BOLD)),
            Span::raw(&info.target),
            Span::raw("  logs: "),
            Span::styled(&self.service, Style::default().fg(Color::Cyan)),
        ];
        if self.structured_rendering() {
            header.push(Span::styled("  · json", Style::default().fg(Color::Magenta)));
        }
        if self.follow {
            header.push(Span::styled(
                "  · following",
                Style::default().fg(Color::Green),
            ));
        }
        frame.render_widget(Paragraph::new(Line::from(header)), header_area);

        let (log_area, stats_area) = if self.show_stats && body_area.width > STATS_WIDTH + 20 {
            let [log_area, stats_area] =
                Layout::horizontal([Constraint::Min(20), Constraint::Length(STATS_WIDTH)])
                    .areas(body_area);
            (log_area, Some(stats_area))
        } else {
            (body_area, None)
        };

        self.draw_logs(frame, log_area);
        if let Some(area) = stats_area {
            self.draw_stats(frame, area);
        }
        frame.render_widget(Paragraph::new(self.footer()), footer_area);
    }

    fn draw_logs(&mut self, frame: &mut Frame, area: Rect) {
        let block = Block::bordered();
        self.viewport = block.inner(area).height;
        let max = self.max_scroll();
        self.scroll = if self.follow { max } else { self.scroll.min(max) };
        if self.store.is_empty() {
            let message = if self.store.filter().is_some() && self.store.total() > 0 {
                "(no lines match the filter)"
            } else if self.ended.is_some() {
                "(no log output)"
            } else {
                "(waiting for logs …)"
            };
            frame.render_widget(
                Paragraph::new(message.dim()).block(block).centered(),
                area,
            );
            return;
        }
        let structured = self.structured_rendering();
        let lines: Vec<Line> = self
            .store
            .iter_from(self.scroll)
            .take(self.viewport as usize)
            .map(|line| render_line(line, structured, self.query.as_deref()))
            .collect();
        frame.render_widget(Paragraph::new(Text::from(lines)).block(block), area);
    }

    fn draw_stats(&self, frame: &mut Frame, area: Rect) {
        let stats = self.store.stats();
        let mut lines = vec![Line::from(vec![
            Span::raw(format!("{} lines · ", stats.total())),
            Span::styled(
                format!("{} json", stats.parsed()),
                Style::default().fg(Color::Magenta),
            ),
        ])];

        lines.push(Line::raw(""));
        lines.push(Line::styled("levels", Style::default().add_modifier(Modifier::BOLD)));
        let levels = stats.level_counts();
        if levels.is_empty() {
            lines.push(Line::raw("  (none)").dim());
        }
        for (level, count) in levels {
            lines.push(Line::from(vec![
                Span::raw(format!("{count:>7}  ")),
                Span::styled(level.to_string(), Style::default().fg(level_color(level))),
            ]));
        }

        lines.push(Line::raw(""));
        match stats.field() {
            Some(field) => {
                lines.push(Line::styled(
                    format!("top {field}"),
                    Style::default().add_modifier(Modifier::BOLD),
                ));
                let values = stats.top_values(TOP_VALUES);
                if values.is_empty() {
                    lines.push(Line::raw("  (no values)").dim());
                }
                for (value, count) in values {
                    lines.push(Line::from(vec![
                        Span::raw(format!("{count:>7}  ")),
                        Span::raw(value.to_string()),
                    ]));
                }
            }
            None => lines.push(Line::raw("t: pick a top field").dim()),
        }

        frame.render_widget(
            Paragraph::new(Text::from(lines)).block(Block::bordered().title(" stats ")),
            area,
        );
    }

    fn footer(&self) -> Line<'_> {
        if let Some((mode, text)) = &self.input {
            let (prompt, hint) = match mode {
                InputMode::Search => (" /", "  enter search · esc cancel"),
                InputMode::Filter => (
                    " filter: ",
                    "  key=value key!=value · empty clears · esc cancel",
                ),
                InputMode::TopField => (" top field: ", "  empty clears · esc cancel"),
            };
            return Line::from(vec![
                Span::raw(prompt),
                Span::raw(text.as_str()),
                Span::styled("▏", Style::default().add_modifier(Modifier::SLOW_BLINK)),
                Span::styled(hint, Style::default().add_modifier(Modifier::DIM)),
            ]);
        }
        if let Some(notice) = &self.notice {
            return Line::from(vec![
                Span::raw(" "),
                Span::styled(notice.as_str(), Style::default().fg(Color::Yellow)),
            ]);
        }
        if let Some(exit_code) = &self.ended {
            let (text, color) = match exit_code {
                Some(0) => ("log stream ended".to_string(), Color::Yellow),
                Some(code) => (format!("log stream ended (exit {code})"), Color::Red),
                None => ("log stream ended".to_string(), Color::Yellow),
            };
            let mut spans = vec![Span::raw(" "), Span::styled(text, Style::default().fg(color))];
            if let Some(stderr) = &self.stderr_notice {
                spans.push(Span::styled(
                    format!("  · {stderr}"),
                    Style::default().fg(Color::Red),
                ));
            }
            return Line::from(spans);
        }
        let mut spans = vec![match self.store.filter() {
            Some(_) => Span::raw(format!(
                " {}/{} lines",
                self.store.len(),
                self.store.total()
            )),
            None => Span::raw(format!(" {} lines", self.store.len())),
        }];
        if let Some(filter) = self.store.filter() {
            spans.push(Span::styled(
                format!("  f:{}", filter.expr()),
                Style::default().fg(Color::Cyan),
            ));
        }
        if let Some(query) = &self.query {
            spans.push(Span::styled(
                format!("  /{query}"),
                Style::default().fg(Color::Yellow),
            ));
        }
        spans.push(Span::styled(
            "  ·  / search · f filter · s json · a stats · t field · esc back",
            Style::default().add_modifier(Modifier::DIM),
        ));
        Line::from(spans)
    }
}

/// Renders one log line: parsed JSONL layout in structured mode, raw text
/// with search highlighting otherwise.
fn render_line(line: &LogLine, structured: bool, query: Option<&str>) -> Line<'static> {
    let record = match &line.record {
        Some(record) if structured => record,
        _ => return Line::from(highlight(&line.raw, query)),
    };
    let mut spans = Vec::new();
    if let Some(timestamp) = record.timestamp() {
        spans.push(Span::styled(
            format!("{timestamp} "),
            Style::default().add_modifier(Modifier::DIM),
        ));
    }
    if let Some(level) = record.level() {
        spans.push(Span::styled(
            format!("{level:<5} "),
            Style::default()
                .fg(level_color(level))
                .add_modifier(Modifier::BOLD),
        ));
    }
    if let Some(message) = record.message() {
        spans.extend(highlight(message, query));
    }
    for (key, value) in record.fields() {
        if !linqode_logs::Record::is_well_known(key) {
            spans.push(Span::styled(
                format!(" {key}={value}"),
                Style::default().add_modifier(Modifier::DIM),
            ));
        }
    }
    if spans.is_empty() {
        return Line::from(highlight(&line.raw, query));
    }
    Line::from(spans)
}

/// Color for a JSONL severity value (any case).
fn level_color(level: &str) -> Color {
    if level.eq_ignore_ascii_case("error")
        || level.eq_ignore_ascii_case("fatal")
        || level.eq_ignore_ascii_case("critical")
        || level.eq_ignore_ascii_case("panic")
    {
        Color::Red
    } else if level.eq_ignore_ascii_case("warn") || level.eq_ignore_ascii_case("warning") {
        Color::Yellow
    } else if level.eq_ignore_ascii_case("info") {
        Color::Green
    } else if level.eq_ignore_ascii_case("debug") {
        Color::Blue
    } else if level.eq_ignore_ascii_case("trace") {
        Color::DarkGray
    } else {
        Color::White
    }
}

/// Splits `text` into spans, highlighting every occurrence of `query`.
fn highlight(text: &str, query: Option<&str>) -> Vec<Span<'static>> {
    let Some(query) = query.filter(|q| !q.is_empty()) else {
        return vec![Span::raw(text.to_string())];
    };
    let mut spans = Vec::new();
    let mut rest = text;
    while let Some(offset) = find_ascii_ci(rest, query) {
        let (before, tail) = rest.split_at(offset);
        let (matched, after) = tail.split_at(query.len());
        if !before.is_empty() {
            spans.push(Span::raw(before.to_string()));
        }
        spans.push(Span::styled(
            matched.to_string(),
            Style::default().fg(Color::Black).bg(Color::Yellow),
        ));
        rest = after;
    }
    if spans.is_empty() {
        return vec![Span::raw(text.to_string())];
    }
    if !rest.is_empty() {
        spans.push(Span::raw(rest.to_string()));
    }
    spans
}
