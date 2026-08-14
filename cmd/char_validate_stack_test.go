package cmd

// Characterization of the `validate` stack lifecycle — the other half F0 moves
// out of cmd/validate.go, into internal/stack.
//
// Covered here: setupValidationStack (every ordering and every early return),
// startDBContainer, startApp, portInUse's preflight, loadEnvFileVars,
// waitForHealth, startChrome and the cleanupFn teardown.
//
// Nothing real is started: `docker` is a /bin/sh stub that records its argv,
// the "application" is this very test binary re-executed as
// TestValidateHelperAppServer, and every stack config uses ready_timeout: 1 so
// the health poll gives up in a second instead of thirty.
//
// No t.Parallel(): validateCapture swaps process-global stdout, and stubBin
// uses t.Setenv on PATH.

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/stack"
)

// validateStubDocker installs a `docker` stub that appends its argv to a log
// file (one invocation per line) and then runs script. The returned path is the
// log — goldening it is how the docker contract (container name, -e flags,
// readiness probe, teardown) gets locked without Docker installed.
func validateStubDocker(t *testing.T, script string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "docker-calls.log")
	stubBin(t, "docker", "echo \"$@\" >> '"+logPath+"'\n"+script)
	return logPath
}

// validateReadLog returns a log file's contents, or "(no calls)" when the stub
// was never invoked.
func validateReadLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "(no calls)\n"
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(data) == 0 {
		return "(no calls)\n"
	}
	return string(data)
}

// validateStackCfg is a stack config with the fast timeout every test wants.
func validateStackCfg(startCommand string, port int) config.ValidateConfig {
	return config.ValidateConfig{
		Stack: config.ValidateStackConfig{
			Runtime:      "go",
			StartCommand: startCommand,
			Port:         port,
			ReadyTimeout: 1,
			HealthPath:   "/health",
		},
		Database: config.ValidateDBConfig{Type: "none"},
	}
}

// ── setupValidationStack: early returns ──────────────────────────────────────

// TestCharacterizeStackEmptyStartCommand locks the cheapest failure: no
// database, no env file, no migrations, and an empty start_command. Note it is
// reported by startApp, NOT by an upfront validation — everything before it has
// already run by then.
func TestCharacterizeStackEmptyStartCommand(t *testing.T) {
	f := newFixture(t)
	cfg := validateStackCfg("", 0)

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})
	goldenAssert(t, "validate_stack_empty_start_command",
		scrub(validateTranscript("setupValidationStack — start_command empty", stdout, stderr, err)))
}

// TestCharacterizeStackMissingAppBinary locks the message when start_command
// names something that is not on PATH. This is the shape `corvex validate`
// shows a user whose start command has a typo.
func TestCharacterizeStackMissingAppBinary(t *testing.T) {
	f := newFixture(t)
	cfg := validateStackCfg("corvex-char-missing-app --serve", 0)

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})
	goldenAssert(t, "validate_stack_missing_app_binary",
		scrub(validateTranscript("setupValidationStack — start_command not on PATH", stdout, stderr, err)))
}

// TestCharacterizeStackPortInUse locks the preflight added to stop the
// validator from judging whatever process already owns the port. The port
// number is kernel-assigned, so the golden carries <PORT>.
//
// The blocking listener binds the WILDCARD address on purpose: that is the only
// case portInUse detects on every platform (see TestCharacterizePortInUse for
// the loopback hole).
func TestCharacterizeStackPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	f := newFixture(t)
	cfg := validateStackCfg("corvex-char-missing-app", port)

	var setupErr error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, setupErr = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})
	body := validateTranscript("setupValidationStack — stack.port already bound", stdout, stderr, setupErr)
	goldenAssert(t, "validate_stack_port_in_use", scrub(validateScrubPort(body, port)))
}

// TestCharacterizeStackPortZeroSkipsPreflight documents the corollary: port 0
// disables the preflight entirely (the guard is `Port != 0`), so the stack
// proceeds and later health-checks http://localhost:0 — which can never answer.
func TestCharacterizeStackPortZeroSkipsPreflight(t *testing.T) {
	f := newFixture(t)
	// /bin/sh is always present and exits immediately; the point is that the
	// stack gets past the preflight and dies at the health check instead.
	cfg := validateStackCfg("/bin/sh -c true", 0)

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})
	// "dur" is kept: the "within 1s" in the message is ready_timeout from the
	// config above, not a clock reading.
	goldenAssert(t, "validate_stack_port_zero",
		scrubExcept(validateTranscript("setupValidationStack — port 0 skips the preflight", stdout, stderr, err), "dur"))
}

// ── setupValidationStack: env_file ───────────────────────────────────────────

func TestCharacterizeStackEnvFileMissing(t *testing.T) {
	f := newFixture(t)
	cfg := validateStackCfg("corvex-char-missing-app", 0)
	cfg.Stack.EnvFile = "backend/.env-stg"

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})
	goldenAssert(t, "validate_stack_env_file_missing",
		scrub(validateTranscript("setupValidationStack — env_file does not exist", stdout, stderr, err)))
}

// TestCharacterizeStackEnvFileSourced proves the env_file reaches BOTH the
// migration command and the app process (the reason the feature exists), and
// locks the "sourced stack env_file" log line including the var count.
//
// The two processes dump their own environment to files; the golden shows the
// filtered dump, which is the only deterministic way to observe env plumbing.
func TestCharacterizeStackEnvFileSourced(t *testing.T) {
	f := newFixture(t)
	f.Write("backend/.env-stg", "# a comment\n\nexport CHAR_DB_HOST=stg.db\nCHAR_DB_PORT=5432\nCHAR_QUOTED=\"a value\"\ngarbage line\nCHAR_EMPTY=\n")

	migrateDump := filepath.Join(t.TempDir(), "migrate-env.txt")
	appDump := filepath.Join(t.TempDir(), "app-env.txt")
	stubBin(t, "corvex-char-migrate", "env | grep '^CHAR_' | sort > '"+migrateDump+"'\necho migrating schema\nexit 0")
	// `exec sleep … >/dev/null` matters twice over: the app becomes the process
	// cleanup kills (no shell in between) and it stops holding the captured
	// stdout pipe, which would otherwise keep validateCapture blocked.
	stubBin(t, "corvex-char-app", "env | grep '^CHAR_' | sort > '"+appDump+"'\nexec sleep 30 >/dev/null 2>&1")

	cfg := validateStackCfg("corvex-char-app", 0)
	cfg.Stack.EnvFile = "backend/.env-stg"
	cfg.Database.MigrateCommand = "corvex-char-migrate --latest"

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})

	body := validateTranscript("setupValidationStack — env_file sourced into migrations + app", stdout, stderr, err) +
		"\n--- migration process env (CHAR_* only) ---\n" + validateReadLog(t, migrateDump) +
		"\n--- app process env (CHAR_* only) ---\n" + validateReadLog(t, appDump)
	goldenAssert(t, "validate_stack_env_file_sourced", scrubExcept(body, "dur"))
}

// ── setupValidationStack: migrations ─────────────────────────────────────────

func TestCharacterizeStackMigrationsFail(t *testing.T) {
	f := newFixture(t)
	stubBin(t, "corvex-char-migrate", "echo 'relation already exists' >&2\nexit 1")

	cfg := validateStackCfg("corvex-char-missing-app", 0)
	cfg.Database.MigrateCommand = "corvex-char-migrate"

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})
	goldenAssert(t, "validate_stack_migrations_fail",
		scrub(validateTranscript("setupValidationStack — migrate command exits 1", stdout, stderr, err)))
}

// ── startDBContainer ─────────────────────────────────────────────────────────

// TestCharacterizeStackDBPostgres locks the full docker contract for the
// supported postgres path: the pre-emptive `rm -f`, the `run` argv (container
// name convention included), the pg_isready probe, and the `rm -f` teardown.
//
// Exactly ONE env var is configured on purpose: cfg.Database.Env is a Go map,
// so with two or more the `-e` flags would come out in random order and this
// golden would flake.
func TestCharacterizeStackDBPostgres(t *testing.T) {
	dockerLog := validateStubDocker(t, "exit 0")

	dbCfg := config.ValidateDBConfig{
		Type:  "postgres",
		Image: "postgres:16",
		Env:   map[string]string{"POSTGRES_PASSWORD": "test"},
	}

	var err error
	var stop func()
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		stop, err = stack.StartDBContainer(context.Background(), "alpha", dbCfg)
		if stop != nil {
			stop()
		}
	})

	body := validateTranscript("startDBContainer — postgres, docker exits 0", stdout, stderr, err) +
		fmt.Sprintf("\nstopFn returned: %v\n", stop != nil) +
		"\n--- docker calls (in order) ---\n" + validateReadLog(t, dockerLog)
	goldenAssert(t, "validate_stack_db_postgres", scrub(body))
}

// TestCharacterizeStackDBMysql locks the other supported probe
// (`mysqladmin ping --silent`).
func TestCharacterizeStackDBMysql(t *testing.T) {
	dockerLog := validateStubDocker(t, "exit 0")

	dbCfg := config.ValidateDBConfig{Type: "mysql", Image: "mysql:8"}

	var err error
	var stop func()
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		stop, err = stack.StartDBContainer(context.Background(), "beta", dbCfg)
		if stop != nil {
			stop()
		}
	})

	body := validateTranscript("startDBContainer — mysql, docker exits 0", stdout, stderr, err) +
		"\n--- docker calls (in order) ---\n" + validateReadLog(t, dockerLog)
	goldenAssert(t, "validate_stack_db_mysql", scrub(body))
}

// TestCharacterizeStackDBUnknownType locks the default branch: for a type with
// no readiness probe (mongodb here) startDBContainer returns as soon as
// `docker run` exits — the container is declared ready without any check.
func TestCharacterizeStackDBUnknownType(t *testing.T) {
	dockerLog := validateStubDocker(t, "exit 0")

	dbCfg := config.ValidateDBConfig{Type: "mongodb", Image: "mongo:7"}

	var err error
	var stop func()
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		stop, err = stack.StartDBContainer(context.Background(), "gamma", dbCfg)
		if stop != nil {
			stop()
		}
	})

	body := validateTranscript("startDBContainer — mongodb (no readiness probe)", stdout, stderr, err) +
		"\n--- docker calls (in order) ---\n" + validateReadLog(t, dockerLog)
	goldenAssert(t, "validate_stack_db_unknown_type", scrub(body))
}

// TestCharacterizeStackDBRunFails locks the error text when `docker run`
// fails — the combined output is embedded in the message.
func TestCharacterizeStackDBRunFails(t *testing.T) {
	dockerLog := validateStubDocker(t, `case "$1" in
run) echo "Unable to find image 'postgres:16' locally"; echo "denied" >&2; exit 125 ;;
esac
exit 0`)

	dbCfg := config.ValidateDBConfig{Type: "postgres", Image: "postgres:16"}

	var err error
	var stop func()
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		stop, err = stack.StartDBContainer(context.Background(), "alpha", dbCfg)
		if stop != nil {
			stop()
		}
	})

	body := validateTranscript("startDBContainer — docker run exits 125", stdout, stderr, err) +
		fmt.Sprintf("\nstopFn returned: %v\n", stop != nil) +
		"\n--- docker calls (in order) ---\n" + validateReadLog(t, dockerLog)
	goldenAssert(t, "validate_stack_db_run_fails", scrub(body))
}

// TestCharacterizeStackDBAbortsSetup wires the same failure through
// setupValidationStack, to lock that the database is step 1 and that its
// failure propagates unwrapped (no "stack setup" prefix at this level).
func TestCharacterizeStackDBAbortsSetup(t *testing.T) {
	dockerLog := validateStubDocker(t, `case "$1" in
run) echo "Cannot connect to the Docker daemon" >&2; exit 1 ;;
esac
exit 0`)
	f := newFixture(t)

	cfg := validateStackCfg("corvex-char-missing-app", 0)
	cfg.Database = config.ValidateDBConfig{Type: "postgres", Image: "postgres:16", MigrateCommand: "corvex-char-migrate"}

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
	})

	body := validateTranscript("setupValidationStack — database container fails first", stdout, stderr, err) +
		"\n--- docker calls (in order) ---\n" + validateReadLog(t, dockerLog)
	goldenAssert(t, "validate_stack_db_aborts_setup", scrub(body))
}

// TestCharacterizeStackSqliteSkipsDocker locks the three database types that
// bypass the container entirely: "", "none" and "sqlite".
func TestCharacterizeStackSqliteSkipsDocker(t *testing.T) {
	var b strings.Builder
	for _, dbType := range []string{"", "none", "sqlite"} {
		dockerLog := validateStubDocker(t, "exit 0")
		f := newFixture(t)
		cfg := validateStackCfg("corvex-char-missing-app", 0)
		cfg.Database = config.ValidateDBConfig{Type: dbType, Image: "postgres:16"}

		var err error
		stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
			var cleanup stack.CleanupFn
			cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
			if cleanup != nil {
				cleanup()
			}
		})
		b.WriteString(validateTranscript(fmt.Sprintf("setupValidationStack — database.type %q", dbType), stdout, stderr, err))
		b.WriteString("\n--- docker calls ---\n" + validateReadLog(t, dockerLog) + "\n")
	}
	goldenAssert(t, "validate_stack_db_skipped", scrub(b.String()))
}

// ── startApp ─────────────────────────────────────────────────────────────────

// TestCharacterizeStartApp locks startApp on its own: the empty-command error,
// the not-on-PATH error, and the fact that the command is split on whitespace
// (so no quoting is possible) and runs with Dir = workDir.
func TestCharacterizeStartApp(t *testing.T) {
	f := newFixture(t)
	cwdDump := filepath.Join(t.TempDir(), "cwd.txt")
	// One line per argument, so the golden shows that strings.Fields shreds a
	// quoted argument into two instead of honouring the quotes.
	stubBin(t, "corvex-char-app", "pwd > '"+cwdDump+"'\nfor a in \"$@\"; do echo \"arg=[$a]\" >> '"+cwdDump+"'; done\nexit 0")

	var b strings.Builder

	for _, cmdStr := range []string{"", "   ", "corvex-char-missing-app", "corvex-char-app --flag 'quoted arg'"} {
		var err error
		var pid int
		stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
			c, e := stack.StartApp(f.Dir, config.ValidateStackConfig{StartCommand: cmdStr}, os.Environ(), validateStreams())
			err = e
			if c != nil && c.Process != nil {
				pid = c.Process.Pid
				_ = c.Wait()
			}
		})
		b.WriteString(validateTranscript(fmt.Sprintf("startApp(start_command=%q)", cmdStr), stdout, stderr, err))
		fmt.Fprintf(&b, "started: %v\n\n", pid > 0)
	}

	// No scrubPath here: the tempdir prefixes in scrub() already cover both the
	// /var/folders and /private/var/folders spellings of pwd's output.
	b.WriteString("--- app pwd + argv ---\n" + validateReadLog(t, cwdDump))
	goldenAssert(t, "validate_start_app", scrub(b.String()))
}

// ── loadEnvFileVars ──────────────────────────────────────────────────────────

// TestCharacterizeLoadEnvFileVars locks the dotenv parser byte for byte: order
// is preserved, `export ` is stripped, one layer of surrounding quotes is
// removed, comments/blank/garbage lines are dropped, an empty value survives,
// and a `#` INSIDE a value is kept (no inline-comment support).
func TestCharacterizeLoadEnvFileVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.env")
	content := strings.Join([]string{
		"# leading comment",
		"",
		"   ",
		"export DB_HOST=stg.db",
		"DB_PORT=5432",
		`DB_PASSWORD="se cret"`,
		`DB_SINGLE='single quoted'`,
		"garbage line without equals",
		"EMPTY=",
		"=novalue",
		"  SPACED_KEY  =  spaced value  ",
		"URL=postgres://u:p@h:5432/db?sslmode=disable",
		"WITH_HASH=value#notacomment",
		"DUPLICATE=first",
		"DUPLICATE=second",
		"#export COMMENTED=1",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing env file: %v", err)
	}

	vars, err := stack.LoadEnvFileVars(path)

	var b strings.Builder
	b.WriteString("# loadEnvFileVars — input\n")
	b.WriteString(content)
	fmt.Fprintf(&b, "\n# loadEnvFileVars — output (%d entries, in order)\n", len(vars))
	for i, v := range vars {
		fmt.Fprintf(&b, "%2d: %q\n", i, v)
	}
	fmt.Fprintf(&b, "\nerror: %v\n", err)

	_, missingErr := stack.LoadEnvFileVars(filepath.Join(dir, "does-not-exist.env"))
	fmt.Fprintf(&b, "\n# missing file\nerror: %v\n", missingErr)

	goldenAssert(t, "validate_load_env_file_vars", scrub(b.String()))
}

// ── waitForHealth ────────────────────────────────────────────────────────────

// TestCharacterizeWaitForHealth locks the readiness rule: any status < 500
// counts as ready (404 included), 5xx keeps polling until the deadline, and a
// dead port fails with the URL and the timeout in the message.
func TestCharacterizeWaitForHealth(t *testing.T) {
	cases := []struct {
		label  string
		status int
	}{
		{"200 OK", http.StatusOK},
		{"404 Not Found is still 'ready'", http.StatusNotFound},
		{"401 Unauthorized is still 'ready'", http.StatusUnauthorized},
		{"500 keeps polling until the deadline", http.StatusInternalServerError},
	}

	var b strings.Builder
	for _, c := range cases {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listening: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
		})}
		go func() { _ = srv.Serve(ln) }()

		cfg := config.ValidateStackConfig{Port: port, HealthPath: "/health", ReadyTimeout: 1}
		hErr := stack.WaitForHealth(context.Background(), cfg)
		_ = srv.Close()

		fmt.Fprintf(&b, "## %s\nerror: %v\n\n", c.label, validateScrubPort(fmt.Sprint(hErr), port))
	}

	// Nothing listening at all.
	dead := validateFreePort(t)
	deadErr := stack.WaitForHealth(context.Background(), config.ValidateStackConfig{Port: dead, HealthPath: "/health", ReadyTimeout: 1})
	fmt.Fprintf(&b, "## nothing listening\nerror: %v\n\n", validateScrubPort(fmt.Sprint(deadErr), dead))

	// Empty health path → "/" is used.
	deadErr = stack.WaitForHealth(context.Background(), config.ValidateStackConfig{Port: dead, ReadyTimeout: 1})
	fmt.Fprintf(&b, "## empty health_path falls back to /\nerror: %v\n\n", validateScrubPort(fmt.Sprint(deadErr), dead))

	// Cancelled context → the first loop iteration returns ctx.Err().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctxErr := stack.WaitForHealth(ctx, config.ValidateStackConfig{Port: dead, ReadyTimeout: 1})
	fmt.Fprintf(&b, "## cancelled context\nerror: %v\n", ctxErr)

	// "dur" is kept: every "within 1s" comes from ready_timeout, not a clock.
	goldenAssert(t, "validate_wait_for_health", scrubExcept(b.String(), "dur"))
}

// ── startChrome ──────────────────────────────────────────────────────────────

// TestCharacterizeStartChromeNoBinary locks the message when none of the four
// candidate binaries is on PATH. PATH is replaced with an empty directory so
// the result never depends on whether the machine has Chrome installed.
func TestCharacterizeStartChromeNoBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		c, e := stack.StartChrome(context.Background())
		err = e
		if c != nil && c.Process != nil {
			_ = c.Process.Kill()
			_ = c.Wait()
		}
	})
	goldenAssert(t, "validate_start_chrome_no_binary",
		scrub(validateTranscript("startChrome — no chromium/chrome on PATH", stdout, stderr, err)))
}

// validateFakeCDP stands in for Chrome's DevTools endpoint on the hardcoded
// port 9222. Returns false when the port is unavailable (a real Chrome is
// probably already debugging there), in which case the caller must skip.
func validateFakeCDP(t *testing.T) bool {
	t.Helper()
	ln, err := net.Listen("tcp", ":9222")
	if err != nil {
		return false
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Browser":"HeadlessChrome/stub","webSocketDebuggerUrl":"ws://localhost:9222/devtools/browser/stub"}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return true
}

// TestCharacterizeStartChromeReady locks the success path: the FIRST candidate
// found on PATH wins (candidate order is chromium, chromium-browser,
// google-chrome, google-chrome-stable), it is launched with the five fixed
// flags, and readiness is decided by a GET to /json/version on port 9222.
func TestCharacterizeStartChromeReady(t *testing.T) {
	if !validateFakeCDP(t) {
		t.Skip("port 9222 is already taken — cannot fake the CDP endpoint")
	}
	argvDump := filepath.Join(t.TempDir(), "chromium-argv.txt")
	// Both candidates exist; the golden proves `chromium` is the one chosen.
	stubBin(t, "google-chrome", "echo google-chrome >> '"+argvDump+"'\nexec sleep 30 >/dev/null 2>&1")
	stubBin(t, "chromium", "for a in \"$@\"; do echo \"arg=[$a]\" >> '"+argvDump+"'; done\nexec sleep 30 >/dev/null 2>&1")

	var err error
	var started bool
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		c, e := stack.StartChrome(context.Background())
		err = e
		if c != nil && c.Process != nil {
			started = true
			// The fake CDP answers on the first poll, which can happen before
			// the stub shell has written its argv — wait for the LAST expected
			// flag before killing it, or this golden truncates at random.
			validateWaitFile(t, argvDump, "--disable-dev-shm-usage")
			_ = c.Process.Kill()
			_ = c.Wait()
		}
	})

	body := validateTranscript("startChrome — chromium on PATH, CDP answering", stdout, stderr, err) +
		fmt.Sprintf("\nprocess returned: %v\n", started) +
		"\n--- chrome argv ---\n" + validateReadLog(t, argvDump)
	goldenAssert(t, "validate_start_chrome_ready", scrub(body))
}

// ── the full stack: success and teardown ─────────────────────────────────────

// TestCharacterizeStackFullSuccess is the only test that takes
// setupValidationStack all the way to a nil error: a real app process, bound to
// a real port, answering a real health check. The app is this test binary
// re-executed as TestValidateHelperAppServer.
//
// It also locks what cleanup() does: the app is dead and the port is free
// afterwards.
func TestCharacterizeStackFullSuccess(t *testing.T) {
	port := validateFreePort(t)
	cfg := validateStackCfg(validateHelperStartCommand(t, port), port)
	cfg.Stack.ReadyTimeout = 10 // starting a Go binary is slower than a shell
	cfg.Stack.HealthPath = "/health"
	f := newFixture(t)

	var err error
	var openBeforeCleanup, closedAfterCleanup bool
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if err != nil {
			return
		}
		openBeforeCleanup = validatePortOpen(port)
		// MUST run before validateCapture returns: the app inherits the stdout
		// pipe, and a survivor would block the drain forever.
		cleanup()
		closedAfterCleanup = validateWaitPortClosed(port)
	})

	body := validateTranscript("setupValidationStack — full success, then cleanup()", stdout, stderr, err) +
		fmt.Sprintf("\nport open before cleanup: %v\nport closed after cleanup: %v\n", openBeforeCleanup, closedAfterCleanup)
	goldenAssert(t, "validate_stack_full_success", scrub(validateScrubPort(body, port)))
}

// TestCharacterizeStackUIEnabled takes the same live app and turns UI on, so
// the run reaches step 5 (Chrome). PATH is emptied so the outcome is the
// deterministic "no Chrome binary" failure, which is also the only way to
// observe that a failing Chrome tears the healthy app down again.
func TestCharacterizeStackUIEnabled(t *testing.T) {
	port := validateFreePort(t)
	cfg := validateStackCfg(validateHelperStartCommand(t, port), port)
	cfg.Stack.ReadyTimeout = 10
	cfg.UI.Enabled = true
	f := newFixture(t)

	// After the app is started by absolute path, nothing else needs PATH.
	t.Setenv("PATH", t.TempDir())

	var err error
	var closedAfterFailure bool
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if cleanup != nil {
			cleanup()
		}
		closedAfterFailure = validateWaitPortClosed(port)
	})

	body := validateTranscript("setupValidationStack — ui.enabled with no Chrome", stdout, stderr, err) +
		fmt.Sprintf("\napp port closed after the failure: %v\n", closedAfterFailure)
	goldenAssert(t, "validate_stack_ui_enabled", scrub(validateScrubPort(body, port)))
}

// TestCharacterizeStackUISuccess is the complete five-step stack: app up,
// health OK, Chrome up, CDP answering — the only path that reaches the
// "chrome CDP ready" log. Both children are killed by cleanup, in reverse order
// (Chrome first, then the app).
func TestCharacterizeStackUISuccess(t *testing.T) {
	if !validateFakeCDP(t) {
		t.Skip("port 9222 is already taken — cannot fake the CDP endpoint")
	}
	chromePID := filepath.Join(t.TempDir(), "chromium.pid")
	stubBin(t, "chromium", "echo $$ > '"+chromePID+"'\nexec sleep 30 >/dev/null 2>&1")

	port := validateFreePort(t)
	cfg := validateStackCfg(validateHelperStartCommand(t, port), port)
	cfg.Stack.ReadyTimeout = 10
	cfg.UI.Enabled = true
	f := newFixture(t)

	var err error
	var appOpen, appClosed, chromeWasAlive, chromeAlive bool
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		if err != nil {
			return
		}
		appOpen = validatePortOpen(port)
		// Read the pid BEFORE the kill: the fake CDP can answer before the stub
		// shell has written the file, so waiting here is what makes
		// "alive after cleanup" a real observation instead of a missing file.
		chromePIDValue := validateReadPID(t, chromePID)
		chromeWasAlive = validatePIDAlive(chromePIDValue)
		cleanup()
		appClosed = validateWaitPortClosed(port)
		// The stub `exec`s sleep, so the recorded pid IS the process cleanup
		// killed — no shell in between.
		chromeAlive = validatePIDAlive(chromePIDValue)
	})

	body := validateTranscript("setupValidationStack — app + chrome, then cleanup()", stdout, stderr, err) +
		fmt.Sprintf("\napp port open before cleanup: %v\napp port closed after cleanup: %v\nchrome process alive before cleanup: %v\nchrome process alive after cleanup: %v\n",
			appOpen, appClosed, chromeWasAlive, chromeAlive)
	goldenAssert(t, "validate_stack_ui_success", scrub(validateScrubPort(body, port)))
}

// TestCharacterizeStackTeardownLeavesGrandchildren characterizes the CURRENT
// teardown, which is NOT a process-group kill: cleanup calls
// appCmd.Process.Kill(), so only the direct child dies. A start_command that
// spawns its own worker (`npm run start` → node, `sh -c "… &"`) leaves that
// worker running — and holding the port.
//
// LEI 1: this is recorded, not fixed. The assertion below encodes the bug on
// purpose, so the F0 refactor cannot change teardown semantics unnoticed.
func TestCharacterizeStackTeardownLeavesGrandchildren(t *testing.T) {
	f := newFixture(t)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// `exec >/dev/null 2>&1` first: the surviving grandchild must NOT keep the
	// captured stdout pipe open or validateCapture would block until it exits.
	// `wait` is a builtin, so the only process left after the shell is killed is
	// the recorded background sleep.
	stubBin(t, "corvex-char-app", "exec >/dev/null 2>&1\nsleep 30 &\necho $! > '"+pidFile+"'\nwait")

	cfg := validateStackCfg("corvex-char-app", 0) // port 0 → health fails in ~1s

	var err error
	stdout, stderr := validateCapture(t, "", func(*bufio.Reader) {
		var cleanup stack.CleanupFn
		cleanup, err = stack.Setup(context.Background(), f.Dir, "alpha", cfg, validateStreams())
		// On failure setupValidationStack already ran its own cleanup and
		// returns a nil cleanupFn, so this is a no-op here — the kill that
		// matters already happened inside.
		if cleanup != nil {
			cleanup()
		}
	})

	pid := validateReadPID(t, pidFile)
	alive := validatePIDAlive(pid)
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	body := validateTranscript("setupValidationStack — teardown after a spawning start_command", stdout, stderr, err) +
		fmt.Sprintf("\ngrandchild pid recorded: %v\ngrandchild still alive after cleanup(): %v\n", pid > 0, alive)
	goldenAssert(t, "validate_stack_teardown_grandchild", scrubExcept(body, "dur"))

	if !alive {
		t.Errorf("grandchild %d died with the app: teardown semantics changed (today's cleanup only kills the direct child)", pid)
	}
}

// validateWaitFile waits up to 5s for path to contain want. Waiting for
// "non-empty" is NOT enough: the fake CDP answers on the first poll, so the stub
// shell is often still appending its argv line by line when the test reads the
// file — that produced a truncated golden roughly one run in six.
func validateWaitFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("stub never wrote %q into %s", want, path)
}

// validateReadPID waits up to 5s for a pid file written by a stub process. It
// requires the trailing newline `echo` writes, so a half-written pid can never
// be parsed as a (wrong, still-alive) process id.
func validateReadPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(data), "\n") && len(strings.TrimSpace(string(data))) > 0 {
			var pid int
			if _, err := fmt.Sscan(strings.TrimSpace(string(data)), &pid); err == nil {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}

// ── portInUse ────────────────────────────────────────────────────────────────

// TestCharacterizePortInUse complements TestPortInUse in port_test.go with the
// answer that shapes setupValidationStack's preflight: port 0 is NEVER reported
// as in use, because binding :0 always succeeds — which is why the preflight
// guards on `Port != 0` instead of trusting portInUse.
//
// The loopback case is deliberately NOT in the golden: portInUse binds the
// wildcard address, and whether that collides with a socket bound only to
// 127.0.0.1 is a kernel policy (darwin allows it, Linux refuses it). It is
// logged and asserted-by-OS below instead, so a golden never flakes across
// platforms — see bugsObserved: on darwin the preflight cannot see a dev server
// bound to 127.0.0.1, which is the common case it was written to catch.
func TestCharacterizePortInUse(t *testing.T) {
	var b strings.Builder

	fmt.Fprintf(&b, "portInUse(0) with nothing bound: %v\n", stack.PortInUse(0))

	wildcard, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listening on the wildcard address: %v", err)
	}
	wildcardPort := wildcard.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(&b, "portInUse(p) while :p (wildcard) is bound: %v\n", stack.PortInUse(wildcardPort))
	_ = wildcard.Close()
	fmt.Fprintf(&b, "portInUse(p) after the wildcard listener closed: %v\n", stack.PortInUse(wildcardPort))

	goldenAssert(t, "validate_port_in_use", b.String())

	loopback, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	defer loopback.Close()
	loopbackPort := loopback.Addr().(*net.TCPAddr).Port
	detected := stack.PortInUse(loopbackPort)
	t.Logf("portInUse(p) while 127.0.0.1:p is bound = %v (GOOS=%s)", detected, runtime.GOOS)
	if runtime.GOOS == "darwin" && detected {
		t.Errorf("portInUse now detects a loopback-only listener on darwin; the preflight behaviour changed")
	}
}
