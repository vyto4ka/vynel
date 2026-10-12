// Package update updates vynel on this server from the panel or `vynel update`: it runs the
// installer in update mode (the same code as `install.sh --mode update`) as a transient systemd
// unit, so restarting the vynel services does not kill the update itself. The result is kept
// for the panel and the node agent to report.
package update

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/scripts"
)

// Paths, overridable for tests.
var (
	EnvFile    = "/etc/vynel/install.env"          // written by install.sh: branch, release, mirror
	ResultFile = "/var/lib/vynel-update/last.json" // the last update's outcome
	LogFile    = "/var/log/vynel-update.log"
	Unit       = "vynel-update"
)

// Source is where this server takes its updates from.
type Source struct {
	Ref, Release, ReleaseURL, GHProxy string
}

var (
	refRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)
	proxyRe = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~/-]*)?$`)
)

// LoadSource reads EnvFile; a server installed before it existed updates from main.
func LoadSource() Source {
	s := Source{Ref: "main"}
	f, err := os.Open(EnvFile)
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
			if !ok || strings.HasPrefix(k, "#") {
				continue
			}
			v = strings.Trim(v, `'"`)
			switch k {
			case "VYNEL_REF":
				if refRe.MatchString(v) {
					s.Ref = v
				}
			case "VYNEL_RELEASE":
				if refRe.MatchString(v) {
					s.Release = v
				}
			case "VYNEL_RELEASE_URL":
				if proxyRe.MatchString(v) {
					s.ReleaseURL = v
				}
			case "VYNEL_GH_PROXY":
				if proxyRe.MatchString(v) {
					s.GHProxy = v
				}
			}
		}
	}
	if s.Release == "" {
		s.Release = "edge-" + strings.ReplaceAll(s.Ref, "/", "-")
	}
	return s
}

func (s Source) releaseURL() string {
	if s.ReleaseURL != "" {
		return s.ReleaseURL
	}
	return s.GHProxy + "https://github.com/vyto4ka/vynel/releases/download/" + s.Release
}

func (s Source) scriptURL() string {
	return s.GHProxy + "https://raw.githubusercontent.com/vyto4ka/vynel/" + s.Ref + "/scripts/install.sh"
}

func (s Source) env() []string {
	env := []string{"VYNEL_REF=" + s.Ref, "VYNEL_RELEASE=" + s.Release}
	if s.ReleaseURL != "" {
		env = append(env, "VYNEL_RELEASE_URL="+s.ReleaseURL)
	}
	if s.GHProxy != "" {
		env = append(env, "VYNEL_GH_PROXY="+s.GHProxy)
	}
	return env
}

var versionRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,40}$`)

// Latest is the version of the newest build in this server's release ("" with an error when
// GitHub does not answer or the release has no VERSION file yet).
func Latest(ctx context.Context, s Source) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	b, err := get(ctx, s.releaseURL()+"/VERSION", 256)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if !versionRe.MatchString(v) {
		return "", fmt.Errorf("bad VERSION %q", v)
	}
	return v, nil
}

func get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Result is the outcome of the last update on this server.
type Result struct {
	OK      bool   `json:"ok"`
	At      int64  `json:"at"`
	Version string `json:"version,omitempty"` // vynel version after the update
	Error   string `json:"error,omitempty"`   // last lines of the log
}

// LastResult reads ResultFile (nil when there was no update yet).
func LastResult() *Result {
	b, err := os.ReadFile(ResultFile)
	if err != nil {
		return nil
	}
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

func saveResult(r Result) {
	b, _ := json.Marshal(r)
	_ = os.MkdirAll(filepath.Dir(ResultFile), 0o755)
	_ = os.WriteFile(ResultFile, b, 0o644)
}

// ErrRunning means an update is going on already.
var ErrRunning = errors.New("an update is already running")

// Start runs the update in the background as the transient unit Unit and returns at once.
func Start(ctx context.Context) error {
	if exec.Command("systemctl", "is-active", "--quiet", Unit).Run() == nil {
		return ErrRunning
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// A failed run of an earlier update keeps the unit name taken until it is reset.
	_ = exec.Command("systemctl", "reset-failed", Unit).Run()
	out, err := exec.CommandContext(ctx, "systemd-run", "--unit="+Unit, "--collect", "--quiet",
		"--description=vynel update", self, "update", "--foreground").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Run updates this server here and now, writing the installer's output to w and LogFile.
func Run(ctx context.Context, w io.Writer) error {
	s := LoadSource()
	logf, err := os.OpenFile(LogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	out := io.MultiWriter(w, logf)
	fmt.Fprintf(out, "vynel update %s: branch %s, release %s\n", time.Now().Format(time.RFC3339), s.Ref, s.Release)

	// The current installer from the branch (fixes to the update itself arrive with it); the
	// one built into this binary when GitHub does not answer.
	script, err := get(ctx, s.scriptURL(), 4<<20)
	if err != nil || !strings.HasPrefix(string(script), "#!") {
		fmt.Fprintf(out, "cannot download the installer (%v): using the one built into this version\n", err)
		script = scripts.Install
	}
	tmp, err := os.CreateTemp("", "vynel-install-*.sh")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(script); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, "bash", tmp.Name(), "--mode", "update", "--yes")
	cmd.Env = append(append(os.Environ(), "TERM=dumb", "VYNEL_NO_TUI=1"), s.env()...)
	cmd.Stdout, cmd.Stderr = out, out
	runErr := cmd.Run()

	res := Result{OK: runErr == nil, At: time.Now().Unix()}
	if v, err := exec.Command("/usr/local/bin/vynel", "version").Output(); err == nil {
		res.Version = strings.TrimSpace(string(v))
	}
	if runErr != nil {
		res.Error = tail(LogFile, 6)
		saveResult(res)
		// The services may still run the old version without having restarted: restart them
		// so the agent reconnects and the panel learns the update failed.
		for _, u := range []string{"vynel-node", "vynel"} {
			if exec.Command("systemctl", "is-enabled", "--quiet", u).Run() == nil {
				_ = exec.Command("systemctl", "restart", u).Run()
			}
		}
		return fmt.Errorf("update failed: %w", runErr)
	}
	saveResult(res)
	return nil
}

func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// The installer colours its output; the panel shows plain text.
	clean := regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]|\r").ReplaceAllString(string(b), "")
	var lines []string
	for _, l := range strings.Split(clean, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
