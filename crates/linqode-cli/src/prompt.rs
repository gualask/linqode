//! Terminal implementations of the connect-phase prompts. These run before
//! the TUI takes over the screen, so they use plain stderr/stdin, mirroring
//! the OpenSSH user experience.

use std::io::{BufRead, Write};
use std::path::Path;

use crossterm::event::{Event, KeyCode, KeyEventKind, KeyModifiers, read};
use crossterm::terminal::{disable_raw_mode, enable_raw_mode};
use linqode_ssh::Prompter;

pub struct TerminalPrompter;

impl Prompter for TerminalPrompter {
    fn confirm_host_key(
        &self,
        host: &str,
        port: u16,
        algorithm: &str,
        fingerprint: &str,
    ) -> std::io::Result<bool> {
        let mut err = std::io::stderr().lock();
        writeln!(
            err,
            "The authenticity of host '{host}:{port}' can't be established.\n\
             {algorithm} key fingerprint is {fingerprint}."
        )?;
        let stdin = std::io::stdin();
        loop {
            write!(
                err,
                "Are you sure you want to continue connecting (yes/no)? "
            )?;
            err.flush()?;
            let mut answer = String::new();
            if stdin.lock().read_line(&mut answer)? == 0 {
                return Ok(false); // EOF on stdin: refuse
            }
            match answer.trim().to_ascii_lowercase().as_str() {
                "yes" | "y" => return Ok(true),
                "no" | "n" => return Ok(false),
                _ => writeln!(err, "Please type 'yes' or 'no'.")?,
            }
        }
    }

    fn ask_passphrase(&self, key_path: &Path) -> std::io::Result<Option<String>> {
        let mut err = std::io::stderr().lock();
        write!(
            err,
            "Enter passphrase for key '{}' (empty to skip): ",
            key_path.display()
        )?;
        err.flush()?;
        let entered = read_secret()?;
        writeln!(err)?;
        Ok(entered.filter(|s| !s.is_empty()))
    }
}

/// Reads a line without echoing it. `None` on Esc or Ctrl-C.
fn read_secret() -> std::io::Result<Option<String>> {
    enable_raw_mode()?;
    let result = read_secret_raw();
    disable_raw_mode()?;
    result
}

fn read_secret_raw() -> std::io::Result<Option<String>> {
    let mut secret = String::new();
    loop {
        let Event::Key(key) = read()? else { continue };
        if key.kind != KeyEventKind::Press {
            continue;
        }
        match key.code {
            KeyCode::Enter => return Ok(Some(secret)),
            KeyCode::Esc => return Ok(None),
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return Ok(None);
            }
            KeyCode::Backspace => {
                secret.pop();
            }
            KeyCode::Char(c) => secret.push(c),
            _ => {}
        }
    }
}
