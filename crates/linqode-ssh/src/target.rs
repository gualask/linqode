use std::path::PathBuf;

use ssh2_config::{ParseRule, SshConfig};

use crate::Error;

/// Default identity file names tried in order, mirroring OpenSSH.
const DEFAULT_IDENTITIES: &[&str] = &["id_ed25519", "id_ecdsa", "id_rsa"];

/// A fully resolved connection target.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Target {
    /// Host name or address to connect to (after `~/.ssh/config` resolution).
    pub host: String,
    /// Name used for `known_hosts` lookup and display: the alias or host as
    /// the user typed it, before `HostName` substitution.
    pub display_host: String,
    pub port: u16,
    pub user: String,
    /// Identity files to try after the agent, in order. Only existing files.
    pub identity_files: Vec<PathBuf>,
}

/// The user/host/port split of a raw spec, before ssh_config resolution.
#[derive(Debug, PartialEq, Eq)]
struct HostSpec {
    user: Option<String>,
    host: String,
    port: Option<u16>,
}

impl HostSpec {
    /// Parses `[user@]host[:port]`.
    fn parse(spec: &str) -> Result<Self, Error> {
        let bad = || Error::BadHostSpec(spec.to_string());
        // Like OpenSSH, the user part ends at the last `@`.
        let (user, rest) = match spec.rsplit_once('@') {
            Some((u, r)) => {
                if u.is_empty() {
                    return Err(bad());
                }
                (Some(u.to_string()), r)
            }
            None => (None, spec),
        };
        let (host, port) = match rest.rsplit_once(':') {
            // A second `:` before the split means a bare IPv6 address, not a port.
            Some((h, _)) if h.contains(':') => (rest, None),
            Some((h, p)) => (h, Some(p.parse::<u16>().map_err(|_| bad())?)),
            None => (rest, None),
        };
        if host.is_empty() {
            return Err(bad());
        }
        Ok(Self {
            user,
            host: host.to_string(),
            port,
        })
    }
}

impl Target {
    /// Resolves a host spec against `~/.ssh/config` and local defaults.
    ///
    /// Explicit user/port in the spec win over ssh_config values, which win
    /// over defaults (local user name, port 22, `~/.ssh/id_*` identities).
    pub fn resolve(spec: &str) -> Result<Self, Error> {
        let ssh_config = SshConfig::parse_default_file(ParseRule::ALLOW_UNKNOWN_FIELDS)
            .unwrap_or_default();
        Self::resolve_with(spec, &ssh_config, home_dir())
    }

    fn resolve_with(
        spec: &str,
        ssh_config: &SshConfig,
        home: Option<PathBuf>,
    ) -> Result<Self, Error> {
        let parsed = HostSpec::parse(spec)?;
        let params = ssh_config.query(&parsed.host);

        let host = params.host_name.unwrap_or_else(|| parsed.host.clone());
        let port = parsed.port.or(params.port).unwrap_or(22);
        let user = parsed
            .user
            .or(params.user)
            .or_else(local_user)
            .ok_or(Error::NoUser)?;

        let mut identity_files: Vec<PathBuf> = params.identity_file.unwrap_or_default();
        if identity_files.is_empty()
            && let Some(home) = home
        {
            identity_files = DEFAULT_IDENTITIES
                .iter()
                .map(|name| home.join(".ssh").join(name))
                .collect();
        }
        identity_files.retain(|p| p.is_file());

        Ok(Self {
            host,
            display_host: parsed.host,
            port,
            user,
            identity_files,
        })
    }
}

fn local_user() -> Option<String> {
    std::env::var("USER")
        .or_else(|_| std::env::var("USERNAME"))
        .ok()
        .filter(|u| !u.is_empty())
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME").map(PathBuf::from)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_bare_host() {
        let s = HostSpec::parse("example.com").unwrap();
        assert_eq!(
            s,
            HostSpec {
                user: None,
                host: "example.com".into(),
                port: None
            }
        );
    }

    #[test]
    fn parses_full_spec() {
        let s = HostSpec::parse("deploy@203.0.113.10:2222").unwrap();
        assert_eq!(
            s,
            HostSpec {
                user: Some("deploy".into()),
                host: "203.0.113.10".into(),
                port: Some(2222)
            }
        );
    }

    #[test]
    fn user_part_ends_at_last_at_sign() {
        let s = HostSpec::parse("we@ird@host").unwrap();
        assert_eq!(s.user.as_deref(), Some("we@ird"));
        assert_eq!(s.host, "host");
    }

    #[test]
    fn bare_ipv6_is_not_a_port() {
        let s = HostSpec::parse("root@fe80::1").unwrap();
        assert_eq!(s.host, "fe80::1");
        assert_eq!(s.port, None);
    }

    #[test]
    fn rejects_bad_specs() {
        for spec in ["", "@host", "user@", "host:", "host:notaport", "user@:22"] {
            assert!(HostSpec::parse(spec).is_err(), "should reject `{spec}`");
        }
    }

    #[test]
    fn resolves_alias_from_ssh_config() {
        let raw = "Host myapp-prod\n  HostName 203.0.113.10\n  User deploy\n  Port 2200\n";
        let mut reader = std::io::BufReader::new(raw.as_bytes());
        let config = SshConfig::default()
            .parse(&mut reader, ParseRule::ALLOW_UNKNOWN_FIELDS)
            .unwrap();

        let t = Target::resolve_with("myapp-prod", &config, None).unwrap();
        assert_eq!(t.host, "203.0.113.10");
        assert_eq!(t.display_host, "myapp-prod");
        assert_eq!(t.port, 2200);
        assert_eq!(t.user, "deploy");
    }

    #[test]
    fn spec_overrides_ssh_config() {
        let raw = "Host myapp-prod\n  HostName 203.0.113.10\n  User deploy\n  Port 2200\n";
        let mut reader = std::io::BufReader::new(raw.as_bytes());
        let config = SshConfig::default()
            .parse(&mut reader, ParseRule::ALLOW_UNKNOWN_FIELDS)
            .unwrap();

        let t = Target::resolve_with("root@myapp-prod:22", &config, None).unwrap();
        assert_eq!(t.host, "203.0.113.10");
        assert_eq!(t.port, 22);
        assert_eq!(t.user, "root");
    }

    #[test]
    fn default_identities_only_existing_files() {
        let home = tempfile::tempdir().unwrap();
        let ssh_dir = home.path().join(".ssh");
        std::fs::create_dir_all(&ssh_dir).unwrap();
        std::fs::write(ssh_dir.join("id_ed25519"), "fake").unwrap();

        let config = SshConfig::default();
        let t = Target::resolve_with(
            "user@example.com",
            &config,
            Some(home.path().to_path_buf()),
        )
        .unwrap();
        assert_eq!(t.identity_files, vec![ssh_dir.join("id_ed25519")]);
    }
}
