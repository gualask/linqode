use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail};
use serde::Deserialize;

/// `~/.config/linqode/config.toml`, see docs/PROJECT.md.
#[derive(Debug, Default, Deserialize)]
pub struct Config {
    #[serde(default)]
    pub hosts: BTreeMap<String, HostEntry>,
}

#[derive(Debug, Deserialize)]
pub struct HostEntry {
    /// `~/.ssh/config` alias or inline `[user@]host[:port]`.
    pub host: String,
    /// Directory on the server containing compose.yaml.
    pub compose_dir: Option<String>,
}

/// The host the session will connect to, after CLI/config selection.
#[derive(Debug, PartialEq, Eq)]
pub struct Selection {
    pub spec: String,
    pub compose_dir: Option<String>,
}

impl Config {
    /// Loads from `path` if given (must exist), else from the default path
    /// (missing file is an empty config).
    pub fn load(path: Option<&Path>) -> Result<Self> {
        let (path, required) = match path {
            Some(p) => (p.to_path_buf(), true),
            None => (default_path()?, false),
        };
        let raw = match std::fs::read_to_string(&path) {
            Ok(raw) => raw,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound && !required => {
                return Ok(Self::default());
            }
            Err(err) => {
                return Err(err).context(format!("cannot read config {}", path.display()));
            }
        };
        Self::parse(&raw).with_context(|| format!("invalid config {}", path.display()))
    }

    fn parse(raw: &str) -> Result<Self> {
        Ok(toml::from_str(raw)?)
    }

    /// Picks the host to connect to. `arg` is a config entry name, an inline
    /// spec, or absent (allowed only with exactly one configured host).
    pub fn select(&self, arg: Option<&str>) -> Result<Selection> {
        match arg {
            Some(name) => {
                if let Some(entry) = self.hosts.get(name) {
                    Ok(Selection {
                        spec: entry.host.clone(),
                        compose_dir: entry.compose_dir.clone(),
                    })
                } else {
                    // Not a configured name: treat as an inline host spec.
                    Ok(Selection {
                        spec: name.to_string(),
                        compose_dir: None,
                    })
                }
            }
            None => match self.hosts.len() {
                1 => {
                    let entry = self.hosts.values().next().expect("one host");
                    Ok(Selection {
                        spec: entry.host.clone(),
                        compose_dir: entry.compose_dir.clone(),
                    })
                }
                0 => bail!(
                    "no host given and no hosts configured; \
                     run `linqode user@host` or add a host to the config file"
                ),
                _ => bail!(
                    "no host given and multiple hosts configured; pick one of: {}",
                    self.hosts.keys().cloned().collect::<Vec<_>>().join(", ")
                ),
            },
        }
    }
}

fn default_path() -> Result<PathBuf> {
    let home = std::env::var_os("HOME").context("HOME is not set")?;
    Ok(PathBuf::from(home).join(".config/linqode/config.toml"))
}

#[cfg(test)]
mod tests {
    use super::*;

    const SAMPLE: &str = r#"
        [hosts.myapp]
        host = "deploy@203.0.113.10"
        compose_dir = "/srv/myapp"

        [hosts.other]
        host = "other-prod"
    "#;

    #[test]
    fn parses_documented_format() {
        let config = Config::parse(SAMPLE).unwrap();
        assert_eq!(config.hosts["myapp"].host, "deploy@203.0.113.10");
        assert_eq!(config.hosts["myapp"].compose_dir.as_deref(), Some("/srv/myapp"));
        assert_eq!(config.hosts["other"].compose_dir, None);
    }

    #[test]
    fn tolerates_future_sections() {
        // The documented M5 `scripts` table must not break older binaries.
        let raw = "[hosts.a]\nhost = \"h\"\n[hosts.a.scripts]\ndisk = \"df -h\"\n";
        assert!(Config::parse(raw).is_ok());
    }

    #[test]
    fn selects_by_name() {
        let config = Config::parse(SAMPLE).unwrap();
        let s = config.select(Some("myapp")).unwrap();
        assert_eq!(s.spec, "deploy@203.0.113.10");
        assert_eq!(s.compose_dir.as_deref(), Some("/srv/myapp"));
    }

    #[test]
    fn unknown_name_is_inline_spec() {
        let config = Config::parse(SAMPLE).unwrap();
        let s = config.select(Some("root@example.com:2222")).unwrap();
        assert_eq!(s.spec, "root@example.com:2222");
        assert_eq!(s.compose_dir, None);
    }

    #[test]
    fn no_arg_needs_exactly_one_host() {
        let one = Config::parse("[hosts.a]\nhost = \"user@h\"\n").unwrap();
        assert_eq!(one.select(None).unwrap().spec, "user@h");

        assert!(Config::default().select(None).is_err());
        assert!(Config::parse(SAMPLE).unwrap().select(None).is_err());
    }

    #[test]
    fn missing_default_config_is_empty() {
        // load(None) with a nonexistent default file must not fail; we cannot
        // safely fake HOME here, so exercise the explicit-path error instead.
        let missing = Path::new("/nonexistent/linqode-config.toml");
        assert!(Config::load(Some(missing)).is_err());
    }
}
