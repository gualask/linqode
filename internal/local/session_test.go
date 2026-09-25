//go:build !windows

package local_test

// The production Executor against real processes on this machine. Every test
// bounds its waits with a guard timeout and leaves nothing running.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gualask/linqode/internal/local"
	"github.com/gualask/linqode/internal/operations"
	"github.com/gualask/linqode/internal/remote"
)

// A local session is what operations asks for, and this is the assertion the
// whole package exists to satisfy.
var _ operations.Executor = (*local.Session)(nil)

const guardTimeout = 10 * time.Second

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	t.Cleanup(cancel)
	return ctx
}

func TestExecCollectsOutputAndExitCode(t *testing.T) {
	session := local.New()
	for _, tc := range []struct {
		name     string
		command  string
		stdout   string
		stderr   string
		exitCode int
	}{
		{"success", "echo hello", "hello\n", "", 0},
		{"both streams", "echo out; echo err >&2", "out\n", "err\n", 0},
		{"failure", "echo err >&2; exit 3", "", "err\n", 3},
		{"command not found", "linqode-no-such-command", "", "", 127},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := session.Exec(testContext(t), tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if string(out.Stdout) != tc.stdout {
				t.Errorf("stdout %q, want %q", out.Stdout, tc.stdout)
			}
			if tc.stderr != "" && !strings.Contains(string(out.Stderr), strings.TrimSpace(tc.stderr)) {
				t.Errorf("stderr %q, want it to contain %q", out.Stderr, tc.stderr)
			}
			if out.ExitCode != tc.exitCode {
				t.Errorf("exit code %d, want %d", out.ExitCode, tc.exitCode)
			}
		})
	}
}

// The operator's locale must not reach a parser: `sysctl -n vm.loadavg`
// prints a decimal comma on an Italian machine, which the remote path never
// sees because sshd's environment is minimal.
func TestCommandsRunUnderTheCLocale(t *testing.T) {
	t.Setenv("LC_ALL", "it_IT.UTF-8")
	t.Setenv("LANG", "it_IT.UTF-8")

	out, err := local.New().Exec(testContext(t), "echo $LC_ALL")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out.Stdout)); got != "C" {
		t.Errorf("LC_ALL is %q, want C", got)
	}
}

// The remote rule is that scripts and `!` run in the login directory, like
// `ssh host 'command'`.
func TestCommandsRunInTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := local.New().Exec(testContext(t), "pwd")
	if err != nil {
		t.Fatal(err)
	}
	// macOS reports /private/var… for a /var… temp dir; compare what the
	// filesystem says both are.
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out.Stdout)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ran in %q, want %q", got, want)
	}
}

// The environment is otherwise the operator's — without their PATH docker
// would not be found.
func TestTheEnvironmentIsInherited(t *testing.T) {
	t.Setenv("LINQODE_TEST_MARKER", "inherited")

	out, err := local.New().Exec(testContext(t), "echo $LINQODE_TEST_MARKER")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out.Stdout)); got != "inherited" {
		t.Errorf("marker is %q, want inherited", got)
	}
}

func TestExecStreamEmitsOutputThenTheExitCode(t *testing.T) {
	events, err := local.New().ExecStream(testContext(t), "echo one; echo two >&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	exit := -1
	for event := range events {
		switch event.Kind {
		case remote.ExecStdout:
			stdout.Write(event.Data)
		case remote.ExecStderr:
			stderr.Write(event.Data)
		case remote.ExecExit:
			exit = event.ExitCode
		}
	}
	if stdout.String() != "one\n" || stderr.String() != "two\n" {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	if exit != 3 {
		t.Errorf("exit code %d, want 3", exit)
	}
}

// An exited command that left something holding its output pipe — a script
// ending in `daemon &` — must neither hang the caller nor outlive it.
func TestAnExitedCommandDoesNotLeaveItsPipeHolderRunning(t *testing.T) {
	command, childPID := backgroundChild(t, "sleep 30")

	start := time.Now()
	out, err := local.New().Exec(testContext(t), "echo done; "+command)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Stdout) != "done\n" || out.ExitCode != 0 {
		t.Errorf("got %+v, want the command's own output and a zero exit", out)
	}
	if elapsed := time.Since(start); elapsed > guardTimeout/2 {
		t.Errorf("waited %s for a command that had exited", elapsed)
	}
	if !waitGone(childPID(), 2*time.Second) {
		t.Errorf("the background child outlived the command")
	}
}

// backgroundChild is a command whose shell starts a child of its own — the
// shape of any configured script with a pipeline or a `&` — and reports that
// child's pid, which is also how a test knows the command has got going.
// Append `; wait` and the shell stays alive as its parent; leave it off and
// the shell exits, leaving the child holding the output pipe.
func backgroundChild(t *testing.T, child string) (command string, pid func() int) {
	t.Helper()
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	command = child + " & echo $! > " + pidfile

	return command, func() int { return awaitPIDFile(t, pidfile) }
}

// awaitPIDFile waits for a command to report a pid, which is how a test
// knows it has run at least that far, and kills whatever it named at the end
// of the test in case the code under test did not.
func awaitPIDFile(t *testing.T, pidfile string) int {
	t.Helper()
	deadline := time.Now().Add(guardTimeout)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the command never reported a pid")
	return 0
}

// waitGone reports whether pid is gone, allowing for the moment between a
// signal and the parent reaping what it killed.
func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}
