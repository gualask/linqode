//! In-process scripted SSH server.
//!
//! TEST-ONLY code: everything under `tests/` is compiled into the
//! integration-test binaries only, never into the `linqode-ssh` library.
//! This fixture lets the tests in `e2e.rs` exercise the real production
//! client — handshake, known_hosts policy, public-key auth, one-shot exec,
//! streaming exec — without a real host, Docker, or any state outside a
//! per-test temp directory. It does not simulate the remote `docker
//! compose` CLI; end-to-end coverage of that belongs to the planned
//! `tests/fixture/` sshd container (see docs/PROJECT.md).

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::AtomicUsize;
use std::time::Duration;

use linqode_ssh::{ConnectOptions, Prompter, Target};
use russh::keys::ssh_key::LineEnding;
use russh::keys::{Algorithm, PrivateKey, PublicKey};
use russh::server::{self, Auth, Msg, Session};
use russh::{Channel, ChannelId};
use tokio::net::TcpListener;

/// The only user name the test server authenticates.
pub const TEST_USER: &str = "linqode-test";

/// Scripted reply to one exec command, matched by exact command string.
#[derive(Clone, Default)]
pub struct Script {
    /// Chunks written to stdout, in order.
    pub stdout: Vec<Vec<u8>>,
    /// Chunks written to stderr (extended data type 1), after stdout.
    pub stderr: Vec<Vec<u8>>,
    pub exit_code: Option<u32>,
    /// Keep the channel open after writing, like a `logs -f` follower;
    /// it then only ends when the client cancels.
    pub hold_open: bool,
}

impl Script {
    /// A command that prints `stdout` and exits with `exit_code`.
    pub fn output(stdout: &str, exit_code: u32) -> Self {
        Self {
            stdout: vec![stdout.as_bytes().to_vec()],
            exit_code: Some(exit_code),
            ..Self::default()
        }
    }

    /// A follower: prints `stdout` and stays running until cancelled.
    pub fn follow(stdout: &str) -> Self {
        Self {
            stdout: vec![stdout.as_bytes().to_vec()],
            hold_open: true,
            ..Self::default()
        }
    }
}

/// A running SSH server on a loopback port, plus the key material and
/// `known_hosts` file the client under test needs. Everything lives in a
/// temp directory dropped with the struct; the accept loop dies with the
/// test's tokio runtime.
pub struct TestServer {
    pub port: u16,
    /// Identity file accepted for [`TEST_USER`].
    pub client_key: PathBuf,
    dir: tempfile::TempDir,
}

impl TestServer {
    pub async fn spawn(scripts: &[(&str, Script)]) -> Self {
        let dir = tempfile::tempdir().expect("create test dir");

        let host_key =
            PrivateKey::random(&mut rand::rng(), Algorithm::Ed25519).expect("host key");
        let client_key = generate_key_file(&dir.path().join("id_ed25519"));
        let authorized = client_key.public_key().clone();
        // Empty known_hosts: each test starts with no trusted hosts.
        std::fs::write(dir.path().join("known_hosts"), "").expect("create known_hosts");

        let config = Arc::new(server::Config {
            keys: vec![host_key],
            auth_rejection_time: Duration::ZERO,
            auth_rejection_time_initial: Some(Duration::ZERO),
            ..server::Config::default()
        });
        let scripts: Arc<HashMap<String, Script>> = Arc::new(
            scripts
                .iter()
                .map(|(command, script)| ((*command).to_string(), script.clone()))
                .collect(),
        );

        let listener = TcpListener::bind(("127.0.0.1", 0)).await.expect("bind");
        let port = listener.local_addr().expect("local addr").port();
        tokio::spawn(async move {
            loop {
                let Ok((socket, _)) = listener.accept().await else {
                    break;
                };
                let connection = Connection {
                    scripts: Arc::clone(&scripts),
                    authorized: authorized.clone(),
                };
                let config = Arc::clone(&config);
                tokio::spawn(async move {
                    if let Ok(session) = server::run_stream(config, socket, connection).await {
                        let _ = session.await;
                    }
                });
            }
        });

        Self {
            port,
            client_key: dir.path().join("id_ed25519"),
            dir,
        }
    }

    /// A target pointing at this server, authenticating with the accepted
    /// client key.
    pub fn target(&self) -> Target {
        Target {
            host: "127.0.0.1".to_string(),
            display_host: "127.0.0.1".to_string(),
            port: self.port,
            user: TEST_USER.to_string(),
            identity_files: vec![self.client_key.clone()],
        }
    }

    /// Hermetic connect options: `known_hosts` in the temp directory, no
    /// SSH agent.
    pub fn options(&self) -> ConnectOptions {
        ConnectOptions {
            known_hosts_file: Some(self.known_hosts()),
            identities_only: true,
        }
    }

    pub fn known_hosts(&self) -> PathBuf {
        self.dir.path().join("known_hosts")
    }
}

/// Writes a fresh ed25519 private key to `path` and returns it.
pub fn generate_key_file(path: &Path) -> PrivateKey {
    let key = PrivateKey::random(&mut rand::rng(), Algorithm::Ed25519).expect("generate key");
    let openssh = key.to_openssh(LineEnding::LF).expect("serialize key");
    std::fs::write(path, openssh.as_bytes()).expect("write key");
    key
}

/// One connection's server-side handler: fixed-key auth plus scripted exec.
struct Connection {
    scripts: Arc<HashMap<String, Script>>,
    authorized: PublicKey,
}

impl server::Handler for Connection {
    type Error = russh::Error;

    async fn auth_publickey(
        &mut self,
        user: &str,
        public_key: &PublicKey,
    ) -> Result<Auth, Self::Error> {
        if user == TEST_USER && public_key.key_data() == self.authorized.key_data() {
            Ok(Auth::Accept)
        } else {
            Ok(Auth::reject())
        }
    }

    async fn channel_open_session(
        &mut self,
        _channel: Channel<Msg>,
        reply: server::ChannelOpenHandle,
        _session: &mut Session,
    ) -> Result<(), Self::Error> {
        reply.accept().await;
        Ok(())
    }

    async fn exec_request(
        &mut self,
        channel: ChannelId,
        data: &[u8],
        session: &mut Session,
    ) -> Result<(), Self::Error> {
        let command = String::from_utf8_lossy(data).into_owned();
        let Some(script) = self.scripts.get(&command) else {
            session.channel_failure(channel)?;
            session.close(channel)?;
            return Ok(());
        };
        session.channel_success(channel)?;
        for chunk in &script.stdout {
            session.data(channel, chunk.clone())?;
        }
        for chunk in &script.stderr {
            session.extended_data(channel, 1, chunk.clone())?;
        }
        if !script.hold_open {
            if let Some(code) = script.exit_code {
                session.exit_status_request(channel, code)?;
            }
            session.eof(channel)?;
            session.close(channel)?;
        }
        Ok(())
    }
}

/// Prompter that trusts any host key, counting how often it was asked.
#[derive(Default)]
pub struct AcceptHostKey {
    pub prompts: AtomicUsize,
}

impl Prompter for AcceptHostKey {
    fn confirm_host_key(
        &self,
        _host: &str,
        _port: u16,
        _algorithm: &str,
        _fingerprint: &str,
    ) -> std::io::Result<bool> {
        self.prompts.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        Ok(true)
    }

    fn ask_passphrase(&self, _key_path: &Path) -> std::io::Result<Option<String>> {
        Ok(None)
    }
}

/// Prompter that refuses any host key.
pub struct RejectHostKey;

impl Prompter for RejectHostKey {
    fn confirm_host_key(
        &self,
        _host: &str,
        _port: u16,
        _algorithm: &str,
        _fingerprint: &str,
    ) -> std::io::Result<bool> {
        Ok(false)
    }

    fn ask_passphrase(&self, _key_path: &Path) -> std::io::Result<Option<String>> {
        Ok(None)
    }
}

/// Prompter that fails the test if any interaction is requested.
pub struct NoInteraction;

impl Prompter for NoInteraction {
    fn confirm_host_key(
        &self,
        host: &str,
        port: u16,
        _algorithm: &str,
        _fingerprint: &str,
    ) -> std::io::Result<bool> {
        panic!("unexpected host key prompt for {host}:{port}");
    }

    fn ask_passphrase(&self, key_path: &Path) -> std::io::Result<Option<String>> {
        panic!("unexpected passphrase prompt for {}", key_path.display());
    }
}
