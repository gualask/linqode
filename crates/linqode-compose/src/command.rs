/// Quotes `s` as a single POSIX shell word.
fn shell_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', r"'\''"))
}

/// Prefixes `command` with a `cd` into the compose directory, when one is
/// configured (the caller's remote working directory otherwise).
fn in_dir(compose_dir: Option<&str>, command: String) -> String {
    match compose_dir {
        Some(dir) => format!("cd {} && {command}", shell_quote(dir)),
        None => command,
    }
}

/// Builds the remote command listing all services of the compose project in
/// `compose_dir` (the caller's remote working directory when `None`).
///
/// `--format json` is NDJSON on compose >= 2.21 and a JSON array before
/// that; [`crate::parse_ps`] accepts both.
pub fn ps_command(compose_dir: Option<&str>) -> String {
    in_dir(compose_dir, "docker compose ps --all --format json".to_string())
}

/// A lifecycle action on one compose service.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ServiceAction {
    Restart,
    Stop,
    Start,
}

impl ServiceAction {
    /// The `docker compose` subcommand this action runs (also the natural
    /// UI label).
    pub fn verb(self) -> &'static str {
        match self {
            Self::Restart => "restart",
            Self::Stop => "stop",
            Self::Start => "start",
        }
    }
}

/// Builds the remote command applying `action` to one service.
pub fn action_command(compose_dir: Option<&str>, action: ServiceAction, service: &str) -> String {
    in_dir(
        compose_dir,
        format!("docker compose {} {}", action.verb(), shell_quote(service)),
    )
}

/// Builds the remote command following the logs of one service, starting
/// `tail` lines back. `--no-log-prefix` drops the service-name prefix (a
/// single service needs none) and `--no-color` its ANSI styling; whatever
/// the container itself writes passes through untouched.
pub fn logs_command(compose_dir: Option<&str>, service: &str, tail: u32) -> String {
    let logs = format!(
        "docker compose logs --follow --no-color --no-log-prefix --tail {tail} {}",
        shell_quote(service)
    );
    in_dir(compose_dir, logs)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn logs_follows_one_service() {
        assert_eq!(
            logs_command(Some("/srv/myapp"), "web", 200),
            "cd '/srv/myapp' && docker compose logs --follow --no-color \
             --no-log-prefix --tail 200 'web'"
        );
    }

    #[test]
    fn without_dir_runs_in_default_directory() {
        assert_eq!(ps_command(None), "docker compose ps --all --format json");
    }

    #[test]
    fn with_dir_changes_directory_first() {
        assert_eq!(
            ps_command(Some("/srv/myapp")),
            "cd '/srv/myapp' && docker compose ps --all --format json"
        );
    }

    #[test]
    fn actions_target_one_service() {
        assert_eq!(
            action_command(Some("/srv/myapp"), ServiceAction::Restart, "web"),
            "cd '/srv/myapp' && docker compose restart 'web'"
        );
        assert_eq!(
            action_command(None, ServiceAction::Stop, "web"),
            "docker compose stop 'web'"
        );
        assert_eq!(
            action_command(None, ServiceAction::Start, "a b"),
            "docker compose start 'a b'"
        );
    }

    #[test]
    fn quotes_hostile_directories() {
        assert_eq!(
            ps_command(Some("/srv/it's; rm -rf $HOME")),
            r#"cd '/srv/it'\''s; rm -rf $HOME' && docker compose ps --all --format json"#
        );
    }
}
