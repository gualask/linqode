package compose

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The README documents `compose_dir = "~/Dev/myapp"`, and a shell expands
// only a tilde it is not quoted out of. Run through a real `sh` with HOME
// pointing at a temp dir, the `cd` must land in the directory under it.
func TestATildeDirIsTheRemoteHome(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "Dev", "it's my app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		"~/Dev/it's my app":  project,
		"~//Dev/it's my app": project,
		"~":                  home,
		"~/":                 home,
	} {
		out, err := runSh(t, home, inDir(dir, "pwd -P"))
		if err != nil {
			t.Errorf("%q: %v: %s", dir, err, out)
			continue
		}
		real, _ := filepath.EvalSymlinks(want)
		if got := strings.TrimSpace(out); got != real {
			t.Errorf("%q: cd landed in %q, want %q", dir, got, real)
		}
	}
}

// Only the tilde is the shell's; what follows it stays one quoted word.
func TestATildeDirStaysOneWord(t *testing.T) {
	home := t.TempDir()
	out, err := runSh(t, home, "printf '%s\\n' "+QuoteDir("~/a b; echo $(id)"))
	if err != nil {
		t.Fatal(err, out)
	}
	if want := home + "/a b; echo $(id)\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	// `~user/` has no quoted form and is left as literal as it always was.
	if got := QuoteDir("~root/app"); got != "'~root/app'" {
		t.Errorf("got %q", got)
	}
	if got := QuoteDir("/srv/~/app"); got != "'/srv/~/app'" {
		t.Errorf("got %q", got)
	}
}

func runSh(t *testing.T, home, command string) (string, error) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	cmd := exec.Command(sh, "-c", command)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
