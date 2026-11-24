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
//
// Two channels (docs/DEVELOPMENT.md, "Сборки и релизы"):
//   - stable: Release is ReleaseLatest, the newest published GitHub release (v1.2.3). Panels and
//     nodes see a new version only when a release is published on purpose.
//   - dev: Release is edge-<branch>, the build CI makes from every push to that branch. It is for
//     trying a commit on your own server before releasing it.
type Source struct {
	Ref, Release, ReleaseURL, GHProxy string
}

// ReleaseLatest is the Release of the stable channel: GitHub's "latest" release.
const ReleaseLatest = "latest"

const releasesBase = "https://github.com/vyto4ka/vynel/releases"

// Channel names, as `vynel update --channel` and the installer's --channel take them.
const (
	ChannelStable = "stable"
	ChannelDev    = "dev"
)

var (
	refRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)
	proxyRe = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~/-]*)?$`)
	ansiRe  = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]|\r")
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
		// An installer from before channels recorded no release: it followed its branch's builds.
		s.Release = devRelease(s.Ref)
	}
	return s
}

// SaveSource writes the source to EnvFile, the format scripts/install.sh writes and LoadSource
// reads.
func SaveSource(s Source) error {
	var b strings.Builder
	b.WriteString("# written by scripts/install.sh and `vynel update --channel`; read by `vynel update`\n")
	fmt.Fprintf(&b, "VYNEL_REF=%s\nVYNEL_RELEASE=%s\n", s.Ref, s.Release)
	if s.ReleaseURL != "" {
		fmt.Fprintf(&b, "VYNEL_RELEASE_URL=%s\n", s.ReleaseURL)
	}
	if s.GHProxy != "" {
		fmt.Fprintf(&b, "VYNEL_GH_PROXY=%s\n", s.GHProxy)
	}
	if err := os.MkdirAll(filepath.Dir(EnvFile), 0o755); err != nil {
		return err
	}
	tmp := EnvFile + ".new"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, EnvFile)
}

// Channel is the channel this source belongs to: dev for an edge-<branch> build, else stable
// (the latest release, or a pinned v1.2.3).
func (s Source) Channel() string {
	if strings.HasPrefix(s.Release, "edge-") {
		return ChannelDev
	}
	return ChannelStable
}

// WithChannel returns the source of another channel. dev takes a branch (ref; "" keeps the
// current one), stable takes none.
func (s Source) WithChannel(channel, ref string) (Source, error) {
	switch channel {
	case ChannelStable:
		if ref != "" {
			return s, errors.New("the stable channel has no branch: --ref is for --channel dev")
		}
		s.Release, s.ReleaseURL = ReleaseLatest, ""
	case ChannelDev:
		if ref == "" {
			ref = s.Ref
		}
		if !refRe.MatchString(ref) {
			return s, fmt.Errorf("bad branch name %q", ref)
		}
		s.Ref, s.Release, s.ReleaseURL = ref, devRelease(ref), ""
	default:
		return s, fmt.Errorf("unknown channel %q (stable or dev)", channel)
	}
	return s, nil
}

// Follow returns the source of the release the panel follows, so a node updates to the panel's
// version: release is "latest", a pinned v1.2.3 or edge-<branch>, ref its branch ("" keeps the
// node's). The node's mirror stays; a direct release URL is dropped when the release changes.
func (s Source) Follow(release, ref string) (Source, error) {
	if !refRe.MatchString(release) || (ref != "" && !refRe.MatchString(ref)) {
		return s, fmt.Errorf("bad release %q (branch %q)", release, ref)
	}
	if release != s.Release {
		s.Release, s.ReleaseURL = release, ""
	}
	if ref != "" {
		s.Ref = ref
	}
	return s, nil
}

// StartFollowing switches this server to the release the panel follows (Follow; "" = stay) and
// starts the update.
func StartFollowing(ctx context.Context, release, ref string) error {
	if release != "" {
		s := LoadSource()
		next, err := s.Follow(release, ref)
		if err != nil {
			return err
		}
		if next != s {
			if err := SaveSource(next); err != nil {
				return fmt.Errorf("cannot switch to the panel's release %s: %w", release, err)
			}
		}
	}
	return Start(ctx)
}

// devRelease is the tag CI publishes the builds of a branch under.
func devRelease(ref string) string { return "edge-" + strings.ReplaceAll(ref, "/", "-") }

// releaseURL is the directory that holds the release's files (vynel-linux-*, SHA256SUMS,
// VERSION, install.sh).
func (s Source) releaseURL() string {
	if s.ReleaseURL != "" {
		return s.ReleaseURL
	}
	if s.Release == ReleaseLatest {
		return s.GHProxy + releasesBase + "/latest/download"
	}
	return s.GHProxy + releasesBase + "/download/" + s.Release
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

// httpClient bounds every download even when the caller's context has no deadline (`vynel update
// --foreground` has none): a server that stops answering must not hang the update for good.
var httpClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("refusing redirect from HTTPS to plain HTTP")
		}
		return nil
	},
}

func get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Result is the state of the last update on this server: Running while it goes, then the outcome.
type Result struct {
	Running   bool   `json:"running,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	OK        bool   `json:"ok"`
	At        int64  `json:"at"`                // when it finished
	Version   string `json:"version,omitempty"` // vynel version after the update
	Error     string `json:"error,omitempty"`   // last lines of the log
}

// Status is what the panel shows about an update: the record plus the end of the log.
type Status struct {
	Running    bool
	OK         bool
	StartedAt  int64
	FinishedAt int64
	Version    string
	Log        string
}

// staleAfter: an update that has said "running" this long died without writing its outcome.
const staleAfter = 30 * time.Minute

// LocalStatus is the state of this server's last update (nil when there was none).
func LocalStatus() *Status {
	r := LastResult()
	if r == nil {
		return nil
	}
	st := &Status{Running: r.Running, OK: r.OK, StartedAt: r.StartedAt, FinishedAt: r.At, Version: r.Version, Log: tail(LogFile, logLines)}
	if r.Running && time.Since(time.Unix(r.StartedAt, 0)) > staleAfter {
		st.Running, st.OK, st.FinishedAt = false, false, r.StartedAt+int64(staleAfter/time.Second)
		st.Log = strings.TrimSpace(st.Log + "\nthe update stopped without a result (killed or the server rebooted)")
	}
	return st
}

// logLines is how much of the log the panel gets.
const logLines = 400

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
	if err := os.MkdirAll(filepath.Dir(ResultFile), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "update: mkdir %s: %v\n", filepath.Dir(ResultFile), err)
	}
	tmp := ResultFile + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil && os.Rename(tmp, ResultFile) == nil {
		return
	}
	if err := os.WriteFile(ResultFile, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "update: write %s: %v\n", ResultFile, err)
	}
}

func logf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// fanoutWriter writes to primary (the log file) first and also forwards to secondary (stdout)
// without letting a broken stdout pipe stop the log file from being written.
type fanoutWriter struct {
	primary   io.Writer
	secondary io.Writer
}

func (fw fanoutWriter) Write(p []byte) (int, error) {
	n, err := fw.primary.Write(p)
	if fw.secondary != nil {
		_, _ = fw.secondary.Write(p)
	}
	return n, err
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
	started := time.Now().Unix()
	src := LoadSource()
	if mkErr := os.MkdirAll(filepath.Dir(LogFile), 0o755); mkErr != nil {
		fmt.Fprintf(os.Stderr, "update: mkdir %s: %v\n", filepath.Dir(LogFile), mkErr)
	}
	if wrErr := os.WriteFile(LogFile, []byte(fmt.Sprintf("[%s] Запуск службы обновления (%s): канал %s, релиз %s\n",
		time.Now().Format("15:04:05"), Unit, src.Channel(), src.Release)), 0o644); wrErr != nil {
		fmt.Fprintf(os.Stderr, "update: write %s: %v\n", LogFile, wrErr)
	}
	saveResult(Result{Running: true, StartedAt: started})
	// A failed run of an earlier update keeps the unit name taken until it is reset.
	_ = exec.Command("systemctl", "reset-failed", Unit).Run()
	out, err := exec.CommandContext(ctx, "systemd-run", "--unit="+Unit, "--collect", "--quiet",
		"--description=vynel update", self, "update", "--foreground").CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
		if f, ferr := os.OpenFile(LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
			logf(f, "ошибка запуска: %s", msg)
			_ = f.Close()
		}
		saveResult(Result{Running: false, OK: false, StartedAt: started, At: time.Now().Unix(), Error: msg})
		return errors.New(msg)
	}
	return nil
}

// Run updates this server here and now, writing the installer's output to w and LogFile.
func Run(ctx context.Context, w io.Writer) error { return run(ctx, w, LoadSource()) }

func run(ctx context.Context, w io.Writer, s Source) error {
	_ = os.MkdirAll(filepath.Dir(LogFile), 0o755)
	started := time.Now().Unix()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if prev := LastResult(); prev != nil && prev.Running && prev.StartedAt > 0 && time.Since(time.Unix(prev.StartedAt, 0)) < 30*time.Second {
		started = prev.StartedAt
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	logfFile, err := os.OpenFile(LogFile, flags, 0o644)
	if err != nil {
		return err
	}
	defer logfFile.Close()
	out := fanoutWriter{primary: logfFile, secondary: w}
	saveResult(Result{Running: true, StartedAt: started})
	logf(out, "vynel update: канал %s, ветка %s, релиз %s", s.Channel(), s.Ref, s.Release)

	// The installer runs as root, so where it comes from matters. With a release key (signed
	// releases) it is the release's own installer, run only when the signature and its checksum
	// match; a bad signature stops the update. Without a key it is still the release's installer
	// (so fixes to the update itself arrive with the release, and only releases are ever
	// installed from), or the one built into this binary when the release has none or GitHub does
	// not answer.
	key, err := releaseKey()
	if err != nil {
		logf(out, "ошибка ключа релиза: %v", err)
		saveResult(Result{StartedAt: started, At: time.Now().Unix(), Error: err.Error()})
		return err
	}
	var script []byte
	if key != nil {
		logf(out, "проверяем подпись релиза и скачиваем установщик (%s)...", s.releaseURL())
		var note string
		script, note, err = signedInstaller(ctx, s, key)
		if err != nil {
			logf(out, "обновление остановлено: %v", err)
			saveResult(Result{StartedAt: started, At: time.Now().Unix(), Error: err.Error()})
			return err
		}
		logf(out, "%s", note)
	} else {
		logf(out, "релизы без подписи (нет ключа): скачиваем install.sh из %s...", s.releaseURL())
		script, err = get(ctx, s.releaseURL()+"/install.sh", 4<<20)
		if err != nil || !strings.HasPrefix(string(script), "#!") {
			if err == nil {
				err = errors.New("not a script")
			}
			logf(out, "не удалось скачать install.sh (%v): используем встроенный установщик", err)
			script = scripts.Install
		} else {
			logf(out, "install.sh загружен (%.1f КБ)", float64(len(script))/1024)
		}
	}
	tmp, err := os.CreateTemp("", "vynel-install-*.sh")
	if err != nil {
		saveResult(Result{StartedAt: started, At: time.Now().Unix(), Error: err.Error()})
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(script); err != nil {
		tmp.Close()
		saveResult(Result{StartedAt: started, At: time.Now().Unix(), Error: err.Error()})
		return err
	}
	tmp.Close()

	logf(out, "запускаем установщик (--mode update --yes)...")
	cmd := exec.CommandContext(ctx, "bash", tmp.Name(), "--mode", "update", "--yes")
	cmd.Env = append(append(os.Environ(), "TERM=dumb", "VYNEL_NO_TUI=1", "VYNEL_UPDATE_LOG=1"), s.env()...)
	cmd.Stdout, cmd.Stderr = out, out
	runErr := cmd.Run()

	res := Result{OK: runErr == nil, StartedAt: started, At: time.Now().Unix()}
	if v, err := exec.Command("/usr/local/bin/vynel", "version").Output(); err == nil {
		res.Version = strings.TrimSpace(string(v))
	}
	if runErr != nil {
		res.Error = tail(LogFile, 12)
		logf(out, "обновление завершилось с ошибкой: %v", runErr)
		saveResult(res)
		// The services may still run the old version without having restarted: restart them
		// so the agent reconnects and the panel learns the update failed.
		for _, u := range []string{"vynel-node", "vynel"} {
			if exec.Command("systemctl", "is-enabled", "--quiet", u).Run() == nil {
				logf(out, "контрольный перезапуск %s...", u)
				_ = exec.Command("systemctl", "restart", u).Run()
			}
		}
		return fmt.Errorf("update failed: %w", runErr)
	}
	logf(out, "обновление завершено успешно: %s", res.Version)
	saveResult(res)
	return nil
}

func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// The installer colours its output; the panel shows plain text.
	clean := ansiRe.ReplaceAllString(string(b), "")
	var lines []string
	for _, l := range strings.Split(clean, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > 64<<10 {
		out = out[len(out)-64<<10:]
	}
	return out
}
