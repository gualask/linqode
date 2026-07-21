use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use russh::client;
use russh::keys::agent::client::AgentClient;
use russh::keys::{self, HashAlg, PrivateKeyWithHashAlg, PublicKey, ssh_key};
use russh::{ChannelMsg, Disconnect, Sig};
use tokio::sync::{mpsc, oneshot};

use crate::{Error, Target};

/// Interactive decisions needed while connecting. Implemented by the frontend
/// (terminal prompts for the CLI, dialogs once the TUI owns the connect phase).
pub trait Prompter: Send + Sync {
    /// Unknown host: show the key and ask whether to trust it (TOFU).
    fn confirm_host_key(
        &self,
        host: &str,
        port: u16,
        algorithm: &str,
        fingerprint: &str,
    ) -> std::io::Result<bool>;

    /// Passphrase for an encrypted identity file. `None` skips the key.
    fn ask_passphrase(&self, key_path: &Path) -> std::io::Result<Option<String>>;
}

/// Collected output of a one-shot remote command.
#[derive(Debug, Default, Clone)]
pub struct ExecOutput {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub exit_code: Option<u32>,
}

/// Something that can run commands on a remote host. The TUI and future log
/// engine depend on this trait, not on the concrete SSH session, so tests can
/// substitute a fixture-backed fake.
pub trait Remote {
    fn exec(
        &self,
        command: &str,
    ) -> impl Future<Output = Result<ExecOutput, Error>> + Send;
}

/// One chunk of output from a streaming remote command.
#[derive(Debug, Clone)]
pub enum ExecEvent {
    Stdout(Vec<u8>),
    Stderr(Vec<u8>),
    Exit(u32),
}

/// A long-running remote command: events arrive as the command produces
/// output; the channel closes after the final [`ExecEvent::Exit`] (if the
/// remote side reported one).
pub struct ExecStream {
    pub events: mpsc::UnboundedReceiver<ExecEvent>,
    pub cancel: ExecCancel,
}

/// Terminates the remote command of an [`ExecStream`]. Dropping it without
/// calling [`ExecCancel::cancel`] cancels as well, so an abandoned stream
/// never leaks a follower process.
pub struct ExecCancel(oneshot::Sender<()>);

impl ExecCancel {
    pub fn cancel(self) {
        let _ = self.0.send(());
    }
}

/// Knobs for how a session is established. The default matches the MVP
/// policy (OpenSSH parity); overrides exist for special setups and let the
/// integration tests stay hermetic (no touching `~/.ssh` or the user's
/// agent).
#[derive(Debug, Default, Clone)]
pub struct ConnectOptions {
    /// Alternative `known_hosts` file, like OpenSSH's `UserKnownHostsFile`.
    /// `None` uses `~/.ssh/known_hosts`.
    pub known_hosts_file: Option<PathBuf>,
    /// Skip SSH agent authentication and use only the target's identity
    /// files, like OpenSSH's `IdentitiesOnly`.
    pub identities_only: bool,
}

/// What happened during host key verification, recorded by the handler so a
/// failed connect can be turned into a precise error.
#[derive(Debug, Default, Clone, Copy)]
enum HostKeyOutcome {
    #[default]
    Unchecked,
    Accepted,
    Rejected,
    Changed {
        line: usize,
    },
}

struct ClientHandler {
    display_host: String,
    port: u16,
    known_hosts_file: Option<PathBuf>,
    prompter: Arc<dyn Prompter>,
    outcome: Arc<Mutex<HostKeyOutcome>>,
}

impl ClientHandler {
    fn record(&self, outcome: HostKeyOutcome) {
        *self.outcome.lock().expect("host key outcome lock") = outcome;
    }

    fn check_known_hosts(&self, key: &PublicKey) -> Result<bool, keys::Error> {
        match &self.known_hosts_file {
            Some(path) => keys::check_known_hosts_path(&self.display_host, self.port, key, path),
            None => keys::check_known_hosts(&self.display_host, self.port, key),
        }
    }

    fn learn_known_hosts(&self, key: &PublicKey) -> Result<(), keys::Error> {
        match &self.known_hosts_file {
            Some(path) => {
                keys::known_hosts::learn_known_hosts_path(&self.display_host, self.port, key, path)
            }
            None => keys::known_hosts::learn_known_hosts(&self.display_host, self.port, key),
        }
    }

    fn verify(&self, key: &PublicKey) -> Result<bool, std::io::Error> {
        match self.check_known_hosts(key) {
            Ok(true) => {
                self.record(HostKeyOutcome::Accepted);
                Ok(true)
            }
            Ok(false) => {
                let fingerprint = key.fingerprint(ssh_key::HashAlg::Sha256).to_string();
                let accepted = self.prompter.confirm_host_key(
                    &self.display_host,
                    self.port,
                    key.algorithm().as_ref(),
                    &fingerprint,
                )?;
                if accepted {
                    self.learn_known_hosts(key).map_err(std::io::Error::other)?;
                    self.record(HostKeyOutcome::Accepted);
                } else {
                    self.record(HostKeyOutcome::Rejected);
                }
                Ok(accepted)
            }
            Err(keys::Error::KeyChanged { line }) => {
                self.record(HostKeyOutcome::Changed { line });
                Ok(false)
            }
            Err(err) => Err(std::io::Error::other(err)),
        }
    }
}

impl client::Handler for ClientHandler {
    type Error = russh::Error;

    async fn check_server_key(&mut self, key: &PublicKey) -> Result<bool, Self::Error> {
        // The prompter blocks on user input; keep the runtime responsive.
        tokio::task::block_in_place(|| self.verify(key)).map_err(russh::Error::from)
    }
}

/// An established SSH session.
pub struct Session {
    handle: client::Handle<ClientHandler>,
}

impl Session {
    /// Connects and authenticates following the MVP policy: SSH agent
    /// identities first, then the target's identity files.
    pub async fn connect(target: &Target, prompter: Arc<dyn Prompter>) -> Result<Self, Error> {
        Self::connect_with(target, prompter, ConnectOptions::default()).await
    }

    /// [`Session::connect`] with explicit [`ConnectOptions`].
    pub async fn connect_with(
        target: &Target,
        prompter: Arc<dyn Prompter>,
        options: ConnectOptions,
    ) -> Result<Self, Error> {
        let config = Arc::new(client::Config::default());
        let outcome = Arc::new(Mutex::new(HostKeyOutcome::default()));
        let handler = ClientHandler {
            display_host: target.display_host.clone(),
            port: target.port,
            known_hosts_file: options.known_hosts_file.clone(),
            prompter: Arc::clone(&prompter),
            outcome: Arc::clone(&outcome),
        };

        let addr = (target.host.as_str(), target.port);
        let mut handle = match client::connect(config, addr, handler).await {
            Ok(handle) => handle,
            Err(err) => {
                let outcome = *outcome.lock().expect("host key outcome lock");
                return Err(match outcome {
                    HostKeyOutcome::Rejected => Error::HostKeyRejected {
                        host: target.display_host.clone(),
                        port: target.port,
                    },
                    HostKeyOutcome::Changed { line } => Error::HostKeyChanged {
                        host: target.display_host.clone(),
                        port: target.port,
                        line,
                    },
                    _ => err.into(),
                });
            }
        };

        let rsa_hash = handle.best_supported_rsa_hash().await?.flatten();

        if (!options.identities_only && try_agent_auth(&mut handle, target, rsa_hash).await?)
            || try_identity_files(&mut handle, target, rsa_hash, prompter.as_ref()).await?
        {
            return Ok(Self { handle });
        }

        Err(Error::AuthFailed {
            user: target.user.clone(),
            host: target.display_host.clone(),
        })
    }

    /// Starts `command` and streams its output instead of collecting it.
    /// Meant for followers like `docker compose logs -f` that only end when
    /// cancelled.
    ///
    /// On cancel the remote command is sent SIGTERM (honored by modern
    /// sshd) and the channel is closed; a follower that misses the signal
    /// dies of SIGPIPE on its next write.
    pub async fn exec_stream(&self, command: &str) -> Result<ExecStream, Error> {
        let mut channel = self.handle.channel_open_session().await?;
        channel.exec(true, command).await?;

        let (event_tx, events) = mpsc::unbounded_channel();
        let (cancel_tx, mut cancel_rx) = oneshot::channel::<()>();
        tokio::spawn(async move {
            loop {
                tokio::select! {
                    // Fires on explicit cancel and when ExecCancel is dropped.
                    _ = &mut cancel_rx => {
                        let _ = channel.signal(Sig::TERM).await;
                        let _ = channel.close().await;
                        break;
                    }
                    msg = channel.wait() => {
                        let event = match msg {
                            Some(ChannelMsg::Data { data }) => ExecEvent::Stdout(data.to_vec()),
                            Some(ChannelMsg::ExtendedData { data, ext: 1 }) => {
                                ExecEvent::Stderr(data.to_vec())
                            }
                            Some(ChannelMsg::ExitStatus { exit_status }) => {
                                ExecEvent::Exit(exit_status)
                            }
                            Some(_) => continue,
                            None => break,
                        };
                        if event_tx.send(event).is_err() {
                            break; // receiver gone: nobody is watching anymore
                        }
                    }
                }
            }
        });

        Ok(ExecStream {
            events,
            cancel: ExecCancel(cancel_tx),
        })
    }

    /// Closes the session cleanly. Errors on an already-dead connection are
    /// not interesting to callers.
    pub async fn close(&self) {
        let _ = self
            .handle
            .disconnect(Disconnect::ByApplication, "", "en")
            .await;
    }
}

impl Remote for Session {
    /// Runs `command` and collects its output until the channel closes.
    async fn exec(&self, command: &str) -> Result<ExecOutput, Error> {
        let mut channel = self.handle.channel_open_session().await?;
        channel.exec(true, command).await?;
        channel.eof().await?;

        let mut output = ExecOutput::default();
        while let Some(msg) = channel.wait().await {
            match msg {
                ChannelMsg::Data { data } => output.stdout.extend_from_slice(&data),
                ChannelMsg::ExtendedData { data, ext: 1 } => {
                    output.stderr.extend_from_slice(&data);
                }
                ChannelMsg::ExitStatus { exit_status } => output.exit_code = Some(exit_status),
                _ => {}
            }
        }
        Ok(output)
    }
}

async fn try_agent_auth(
    handle: &mut client::Handle<ClientHandler>,
    target: &Target,
    rsa_hash: Option<HashAlg>,
) -> Result<bool, Error> {
    let Ok(mut agent) = AgentClient::connect_env().await else {
        return Ok(false); // no agent running: fall through to identity files
    };
    let Ok(identities) = agent.request_identities().await else {
        return Ok(false);
    };
    for identity in identities {
        // Agent-held certificates are out of scope for the MVP.
        let russh::keys::agent::AgentIdentity::PublicKey { key, .. } = identity else {
            continue;
        };
        let hash = key.algorithm().is_rsa().then_some(rsa_hash).flatten();
        match handle
            .authenticate_publickey_with(&target.user, key, hash, &mut agent)
            .await
        {
            Ok(result) if result.success() => return Ok(true),
            // A refused or failing identity is normal; try the next one.
            _ => continue,
        }
    }
    Ok(false)
}

async fn try_identity_files(
    handle: &mut client::Handle<ClientHandler>,
    target: &Target,
    rsa_hash: Option<HashAlg>,
    prompter: &dyn Prompter,
) -> Result<bool, Error> {
    for path in &target.identity_files {
        let key = match keys::load_secret_key(path, None) {
            Ok(key) => key,
            Err(keys::Error::KeyIsEncrypted) => {
                match load_encrypted_key(path, prompter).await? {
                    Some(key) => key,
                    None => continue, // user skipped this key
                }
            }
            Err(_) => continue, // unreadable or unsupported format: skip
        };
        let result = handle
            .authenticate_publickey(
                &target.user,
                PrivateKeyWithHashAlg::new(Arc::new(key), rsa_hash),
            )
            .await?;
        if result.success() {
            return Ok(true);
        }
    }
    Ok(false)
}

/// Prompts for a passphrase, retrying like OpenSSH before giving up.
async fn load_encrypted_key(
    path: &Path,
    prompter: &dyn Prompter,
) -> Result<Option<ssh_key::PrivateKey>, Error> {
    const ATTEMPTS: u32 = 3;
    for _ in 0..ATTEMPTS {
        let Some(passphrase) = tokio::task::block_in_place(|| prompter.ask_passphrase(path))?
        else {
            return Ok(None);
        };
        match keys::load_secret_key(path, Some(&passphrase)) {
            Ok(key) => return Ok(Some(key)),
            Err(_) => continue,
        }
    }
    Err(Error::BadPassphrase {
        path: path.display().to_string(),
    })
}
