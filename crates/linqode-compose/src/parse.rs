use crate::{Error, Service};

/// Parses the stdout of `docker compose ps --all --format json`.
///
/// Accepts both output shapes: NDJSON (one object per line, compose >= 2.21)
/// and a single JSON array (older releases). Services are sorted by service
/// name so the view is stable across refreshes.
pub fn parse_ps(raw: &[u8]) -> Result<Vec<Service>, Error> {
    let text = String::from_utf8_lossy(raw);
    let trimmed = text.trim();
    let mut services: Vec<Service> = if trimmed.is_empty() {
        Vec::new()
    } else if trimmed.starts_with('[') {
        serde_json::from_str(trimmed)?
    } else {
        trimmed
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty())
            .map(serde_json::from_str)
            .collect::<Result<_, _>>()?
    };
    services.sort_by(|a, b| a.service.cmp(&b.service).then_with(|| a.name.cmp(&b.name)));
    Ok(services)
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Captured from compose v2.27 (NDJSON, trimmed to relevant fields plus
    /// a few we ignore).
    const NDJSON: &str = r#"
{"Command":"\"docker-entrypoint.sh postgres\"","CreatedAt":"2026-07-20 10:00:00 +0000 UTC","ExitCode":0,"Health":"healthy","ID":"0123456789ab","Image":"postgres:16","Name":"myapp-db-1","Project":"myapp","Publishers":[{"URL":"0.0.0.0","TargetPort":5432,"PublishedPort":5432,"Protocol":"tcp"},{"URL":"::","TargetPort":5432,"PublishedPort":5432,"Protocol":"tcp"}],"RunningFor":"2 hours ago","Service":"db","State":"running","Status":"Up 2 hours (healthy)"}
{"Command":"\"/app/server\"","CreatedAt":"2026-07-20 10:00:01 +0000 UTC","ExitCode":1,"Health":"","ID":"ba9876543210","Image":"myapp/web:latest","Name":"myapp-web-1","Project":"myapp","Publishers":null,"RunningFor":"","Service":"web","State":"exited","Status":"Exited (1) 5 minutes ago"}
"#;

    #[test]
    fn parses_ndjson_lines() {
        let services = parse_ps(NDJSON.as_bytes()).unwrap();
        assert_eq!(services.len(), 2);

        let db = &services[0];
        assert_eq!(db.service, "db");
        assert_eq!(db.name, "myapp-db-1");
        assert_eq!(db.state, "running");
        assert_eq!(db.health, "healthy");
        assert_eq!(db.exit_code, Some(0));
        assert_eq!(db.ports_summary(), "5432->5432/tcp");

        let web = &services[1];
        assert_eq!(web.state, "exited");
        assert_eq!(web.exit_code, Some(1));
        assert_eq!(web.publishers, vec![]); // null in the JSON
    }

    #[test]
    fn parses_legacy_json_array() {
        let raw = r#"[{"Name":"a-web-1","Service":"web","State":"running","Status":"Up"},
                      {"Name":"a-db-1","Service":"db","State":"running","Status":"Up"}]"#;
        let services = parse_ps(raw.as_bytes()).unwrap();
        assert_eq!(services.len(), 2);
        // Sorted by service name regardless of input order.
        assert_eq!(services[0].service, "db");
        assert_eq!(services[1].service, "web");
    }

    #[test]
    fn empty_output_means_no_services() {
        assert_eq!(parse_ps(b"").unwrap(), vec![]);
        assert_eq!(parse_ps(b"  \n").unwrap(), vec![]);
    }

    #[test]
    fn rejects_non_json_output() {
        assert!(parse_ps(b"NAME  STATUS\nweb   Up").is_err());
    }
}
