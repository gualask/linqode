use serde::{Deserialize, Deserializer};

/// One service entry from `docker compose ps --format json`.
///
/// Unknown fields are ignored so newer compose releases keep parsing; every
/// field defaults so older releases that omit some keep parsing too.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct Service {
    /// Container name, e.g. `myapp-db-1`.
    #[serde(default)]
    pub name: String,
    /// Compose service name, e.g. `db`.
    #[serde(default)]
    pub service: String,
    /// `running`, `exited`, `restarting`, `paused`, `created`, `dead`.
    #[serde(default)]
    pub state: String,
    /// `healthy`, `unhealthy`, `starting`; empty without a healthcheck.
    #[serde(default)]
    pub health: String,
    #[serde(default)]
    pub exit_code: Option<i64>,
    /// Human-readable status, e.g. `Up 2 hours (healthy)`.
    #[serde(default)]
    pub status: String,
    #[serde(default, deserialize_with = "null_as_default")]
    pub publishers: Vec<Publisher>,
}

/// A port mapping from the `Publishers` array.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct Publisher {
    #[serde(default, rename = "URL")]
    pub url: String,
    #[serde(default)]
    pub target_port: u16,
    /// 0 when the port is exposed but not published on the host.
    #[serde(default)]
    pub published_port: u16,
    #[serde(default)]
    pub protocol: String,
}

impl Service {
    /// Compact port list for display, e.g. `8080->80/tcp, 5432/tcp`.
    /// Collapses the IPv4/IPv6 duplicates compose emits per mapping.
    pub fn ports_summary(&self) -> String {
        let mut parts: Vec<String> = Vec::new();
        for p in &self.publishers {
            let part = if p.published_port != 0 {
                format!("{}->{}/{}", p.published_port, p.target_port, p.protocol)
            } else {
                format!("{}/{}", p.target_port, p.protocol)
            };
            if !parts.contains(&part) {
                parts.push(part);
            }
        }
        parts.join(", ")
    }
}

/// Compose serializes an empty `Publishers` as JSON `null`; treat it as the
/// default value instead of a type error.
fn null_as_default<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Default + Deserialize<'de>,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn publisher(published: u16, target: u16, url: &str) -> Publisher {
        Publisher {
            url: url.to_string(),
            target_port: target,
            published_port: published,
            protocol: "tcp".to_string(),
        }
    }

    #[test]
    fn ports_summary_collapses_ipv4_ipv6_duplicates() {
        let service = Service {
            publishers: vec![
                publisher(8080, 80, "0.0.0.0"),
                publisher(8080, 80, "::"),
                publisher(0, 5432, ""),
            ],
            ..Service::default()
        };
        assert_eq!(service.ports_summary(), "8080->80/tcp, 5432/tcp");
    }

    #[test]
    fn ports_summary_empty_without_publishers() {
        assert_eq!(Service::default().ports_summary(), "");
    }
}
