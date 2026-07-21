//! Integration tests: the production `linqode-ssh` client against the
//! in-process scripted server in `support/`. Loopback only — no Docker, no
//! real host, no state outside per-test temp directories.

mod support;

use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::Duration;

use linqode_ssh::{Error, ExecEvent, ExecStream, Remote, Session};
use support::{AcceptHostKey, NoInteraction, RejectHostKey, Script, TestServer, generate_key_file};
use tokio::time::timeout;

/// Generous upper bound so a hang fails the test instead of blocking CI.
const TIMEOUT: Duration = Duration::from_secs(10);

async fn connect(server: &TestServer) -> Session {
    timeout(
        TIMEOUT,
        Session::connect_with(
            &server.target(),
            Arc::new(AcceptHostKey::default()),
            server.options(),
        ),
    )
    .await
    .expect("connect timed out")
    .expect("connect failed")
}

#[tokio::test(flavor = "multi_thread")]
async fn tofu_accepts_persists_and_reconnects_silently() {
    let server = TestServer::spawn(&[("echo hello", Script::output("hello\n", 0))]).await;

    let prompter = Arc::new(AcceptHostKey::default());
    let session = Session::connect_with(&server.target(), prompter.clone(), server.options())
        .await
        .expect("first connect");
    assert_eq!(prompter.prompts.load(Ordering::SeqCst), 1, "one TOFU prompt");

    let output = session.exec("echo hello").await.expect("exec");
    assert_eq!(String::from_utf8_lossy(&output.stdout), "hello\n");
    assert_eq!(output.exit_code, Some(0));
    session.close().await;

    // The accepted key was persisted for this host:port…
    let known_hosts = std::fs::read_to_string(server.known_hosts()).expect("read known_hosts");
    assert!(
        known_hosts.contains(&format!("[127.0.0.1]:{}", server.port)),
        "known_hosts should list the server, got: {known_hosts:?}"
    );
    // …so reconnecting must not prompt at all.
    let session = Session::connect_with(&server.target(), Arc::new(NoInteraction), server.options())
        .await
        .expect("reconnect");
    session.close().await;
}

#[tokio::test(flavor = "multi_thread")]
async fn refused_host_key_aborts_connect() {
    let server = TestServer::spawn(&[]).await;
    let result =
        Session::connect_with(&server.target(), Arc::new(RejectHostKey), server.options()).await;
    assert!(
        matches!(result, Err(Error::HostKeyRejected { port, .. }) if port == server.port),
        "expected HostKeyRejected"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn changed_host_key_refuses_connect() {
    let server = TestServer::spawn(&[]).await;

    // known_hosts already pins a *different* key for this host:port.
    let impostor = generate_key_file(&server.known_hosts().with_file_name("impostor"));
    let entry = format!(
        "[127.0.0.1]:{} {}\n",
        server.port,
        impostor.public_key().to_openssh().expect("encode key")
    );
    std::fs::write(server.known_hosts(), entry).expect("write known_hosts");

    // Even a prompter that would accept anything must never be consulted.
    let result =
        Session::connect_with(&server.target(), Arc::new(NoInteraction), server.options()).await;
    assert!(
        matches!(result, Err(Error::HostKeyChanged { line: 1, .. })),
        "expected HostKeyChanged at line 1"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn unauthorized_key_fails_auth() {
    let server = TestServer::spawn(&[]).await;

    let rogue_dir = tempfile::tempdir().expect("tempdir");
    let rogue_path = rogue_dir.path().join("id_ed25519");
    generate_key_file(&rogue_path);
    let mut target = server.target();
    target.identity_files = vec![rogue_path];

    let result = Session::connect_with(
        &target,
        Arc::new(AcceptHostKey::default()),
        server.options(),
    )
    .await;
    assert!(
        matches!(result, Err(Error::AuthFailed { ref user, .. }) if user == support::TEST_USER),
        "expected AuthFailed"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn exec_collects_stdout_stderr_and_exit_code() {
    let script = Script {
        stdout: vec![b"chunk one\n".to_vec(), b"chunk two\n".to_vec()],
        stderr: vec![b"a warning\n".to_vec()],
        exit_code: Some(3),
        hold_open: false,
    };
    let server = TestServer::spawn(&[("failing job", script)]).await;
    let session = connect(&server).await;

    let output = timeout(TIMEOUT, session.exec("failing job"))
        .await
        .expect("exec timed out")
        .expect("exec failed");
    assert_eq!(String::from_utf8_lossy(&output.stdout), "chunk one\nchunk two\n");
    assert_eq!(String::from_utf8_lossy(&output.stderr), "a warning\n");
    assert_eq!(output.exit_code, Some(3));
    session.close().await;
}

#[tokio::test(flavor = "multi_thread")]
async fn exec_stream_delivers_events_then_ends() {
    let script = Script {
        stdout: vec![b"out\n".to_vec()],
        stderr: vec![b"err\n".to_vec()],
        exit_code: Some(0),
        hold_open: false,
    };
    let server = TestServer::spawn(&[("short", script)]).await;
    let session = connect(&server).await;

    let mut stream = session.exec_stream("short").await.expect("exec_stream");
    let (mut stdout, mut stderr, mut exit_code) = (Vec::new(), Vec::new(), None);
    while let Some(event) = timeout(TIMEOUT, stream.events.recv())
        .await
        .expect("event timed out")
    {
        match event {
            ExecEvent::Stdout(bytes) => stdout.extend(bytes),
            ExecEvent::Stderr(bytes) => stderr.extend(bytes),
            ExecEvent::Exit(code) => exit_code = Some(code),
        }
    }
    assert_eq!(stdout, b"out\n");
    assert_eq!(stderr, b"err\n");
    assert_eq!(exit_code, Some(0));
    session.close().await;
}

#[tokio::test(flavor = "multi_thread")]
async fn exec_stream_cancel_ends_a_follower() {
    let server = TestServer::spawn(&[("follow", Script::follow("line1\nline2\n"))]).await;
    let session = connect(&server).await;

    let ExecStream { mut events, cancel } =
        session.exec_stream("follow").await.expect("exec_stream");

    // Output arrives while the remote command stays open.
    let first = timeout(TIMEOUT, events.recv())
        .await
        .expect("first event timed out")
        .expect("stream ended prematurely");
    assert!(
        matches!(&first, ExecEvent::Stdout(bytes) if bytes.as_slice() == b"line1\nline2\n"),
        "expected the initial stdout chunk, got {first:?}"
    );

    // Cancelling tears the stream down instead of hanging forever.
    cancel.cancel();
    let end = timeout(TIMEOUT, async {
        loop {
            if events.recv().await.is_none() {
                break;
            }
        }
    })
    .await;
    assert!(end.is_ok(), "stream did not end after cancel");
    session.close().await;
}
