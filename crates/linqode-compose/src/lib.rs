//! Docker Compose remote-command builders and output models.
//!
//! Linqode is agentless: the remote "API" is the `docker compose` CLI. This
//! crate builds the command lines to run over SSH and parses their output
//! into typed models. It performs no I/O itself.

mod command;
mod model;
mod parse;

pub use command::{logs_command, ps_command};
pub use model::{Publisher, Service};
pub use parse::parse_ps;

/// Errors produced by this crate.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("cannot parse `docker compose ps` output: {0}")]
    Parse(#[from] serde_json::Error),
}
