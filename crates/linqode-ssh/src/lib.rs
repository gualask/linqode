//! SSH transport for Linqode.
//!
//! Resolves a target from a host spec (inline `user@host[:port]` or an
//! `~/.ssh/config` alias), connects with OpenSSH-like behavior (agent first,
//! then default identity files; `known_hosts` verification with
//! trust-on-first-use), and runs one-shot remote commands.

mod session;
mod target;

pub use session::{
    ConnectOptions, ExecCancel, ExecEvent, ExecOutput, ExecStream, Prompter, Remote, Session,
};
pub use target::Target;

/// Errors produced by this crate.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("invalid host spec `{0}` (expected [user@]host[:port])")]
    BadHostSpec(String),

    #[error("cannot determine the local user name; pass an explicit user@host")]
    NoUser,

    #[error(
        "host key for {host}:{port} has changed (known_hosts line {line}): \
         possible man-in-the-middle attack, refusing to connect"
    )]
    HostKeyChanged { host: String, port: u16, line: usize },

    #[error("host key for {host}:{port} was not accepted")]
    HostKeyRejected { host: String, port: u16 },

    #[error(
        "authentication failed for {user}@{host}: no SSH agent identity or \
         default key in ~/.ssh was accepted by the server"
    )]
    AuthFailed { user: String, host: String },

    #[error("failed to decrypt key {path}: wrong passphrase?")]
    BadPassphrase { path: String },

    #[error("ssh error: {0}")]
    Ssh(#[from] russh::Error),

    #[error("ssh key error: {0}")]
    Key(#[from] russh::keys::Error),

    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
}
