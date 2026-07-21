/// Quotes `s` as a single POSIX shell word.
fn shell_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', r"'\''"))
}

/// Builds the remote command listing all services of the compose project in
/// `compose_dir` (the caller's remote working directory when `None`).
///
/// `--format json` is NDJSON on compose >= 2.21 and a JSON array before
/// that; [`crate::parse_ps`] accepts both.
pub fn ps_command(compose_dir: Option<&str>) -> String {
    const PS: &str = "docker compose ps --all --format json";
    match compose_dir {
        Some(dir) => format!("cd {} && {PS}", shell_quote(dir)),
        None => PS.to_string(),
    }
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
    match compose_dir {
        Some(dir) => format!("cd {} && {logs}", shell_quote(dir)),
        None => logs,
    }
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
    fn quotes_hostile_directories() {
        assert_eq!(
            ps_command(Some("/srv/it's; rm -rf $HOME")),
            r#"cd '/srv/it'\''s; rm -rf $HOME' && docker compose ps --all --format json"#
        );
    }
}
