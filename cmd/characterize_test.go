package cmd

// Characterization harness for cmd/.
//
// The goal of this file is to make it cheap to lock the CURRENT behaviour of a
// cobra command down to the byte, so a refactor can prove "zero behaviour
// change". Nothing here fixes bugs or normalises output that the CLI itself
// prints — the only thing scrub() removes is genuine non-determinism (clocks,
// temp paths, hashes), because a golden that flakes is a golden nobody trusts.
//
// Usage in one paragraph:
//
//	args := []string{"list"}
//	f := newFixture(t).AddProject("demo", fixtureSpecMD, fixtureTasksMD)
//	out, errOut, err := runCLIIn(t, f.Dir, args...)
//	goldenAssert(t, "list_human", scrub(transcript(args, out, errOut, err)))
//
// Rewrite goldens with:  go test ./cmd/ -run TestCharacterize -update-golden
//
// HARD RULE: none of these helpers is safe under t.Parallel(). They chdir the
// process and call t.Setenv. Never mark a characterization test parallel.
//
// ─────────────────────────────────────────────────────────────────────────────
// COMO RODAR ESTA REDE (F0)
// ─────────────────────────────────────────────────────────────────────────────
//
// Esta rede existe para provar "zero mudanca de comportamento". Rodada errada,
// ela da falso verde — e um falso verde aqui e pior que nao ter rede nenhuma,
// porque a F0 vai commitar acreditando que provou algo. Quatro regras:
//
//  1. SEMPRE o pacote inteiro:
//
//     go test ./cmd/
//
//     Esse e o unico comando cujo verde vale como evidencia de nao-regressao.
//
//  2. NUNCA julgue regressao por subconjunto (-run). O `-run` serve para DUAS
//     coisas e nada mais: iterar rapido enquanto voce mexe num comando, e
//     regravar golden. Ele nao serve para decidir se a fase pode commitar.
//     Toda funcao desta rede usa o prefixo TestCharacterize justamente para que
//     `-run TestCharacterize` cubra 229/229 — mas mesmo assim isso deixa de
//     fora os testes unitarios antigos de cmd/, que tambem travam saida
//     (doctor_test.go, status_test.go, list_test.go, ...). Prefixo consistente
//     resolve o falso verde por regex; nao promove subconjunto a evidencia.
//     Se voce esta prestes a colar `-run` num relatorio, esta errado.
//
//  3. NUNCA GOMAXPROCS=1. O renderer do `run` e drenado numa goroutine que
//     ninguem espera (cmd/run.go:171 — anomalia conhecida, congelada de
//     proposito). Com um unico P essa goroutine quase nunca e escalonada antes
//     do fim da invocacao, o stdout chega vazio, e runAssertRendererLines cai no
//     ramo "nao chegou nada", que LOGA e PASSA. Medido nesta maquina (10 CPUs),
//     200 amostras por linha, contando amostras em que ALGUMA linha do renderer
//     chegou:
//
//     GOMAXPROCS=1,  ociosa           2/200   (p=0.010)
//     GOMAXPROCS=1,  8 CPU hogs       0/200   (p=0)
//     GOMAXPROCS=2,  ociosa         199/200   (p=0.995)
//     GOMAXPROCS=2,  8 CPU hogs       2/200   (p=0.010)
//     GOMAXPROCS=10, ociosa         200/200
//
//     Ou seja: o ramo e sensivel a SATURACAO de CPU, nao so a GOMAXPROCS — numa
//     maquina carregada ele dispara no default tambem, e num container de 1 cpu
//     ele e praticamente garantido. Se `go test ./cmd/ -v` mostrar "no
//     PlainRenderer lines reached stdout", aquele teste nao verificou o stdout do
//     renderer nessa execucao — trate como nao-executado, nao como verde.
//
//     A UNICA excecao e TestCharacterizeRunRendererNotSilenced
//     (char_run_drain_test.go): ele usa runCLIAwait e por isso vale em qualquer
//     escalonamento (medido 800/800, incluindo as duas linhas de p=0 acima). E
//     ele que garante que "o renderer emudeceu" nao passa verde. Se voce so pode
//     confiar em um teste de renderer numa maquina apertada, e nesse.
//
//  4. ANTES DE COMMITAR FASE:
//
//     go test ./cmd/ -count=2
//
//     Dois passes na mesma invocacao pegam ordem-dependencia e estado global
//     vazado entre testes (cwd, env, flags de cobra, goldens regravados no
//     meio do caminho). Uma rede que so passa no primeiro pass nao e rede.
//
// Regravar golden e um ATO DELIBERADO. Golden vermelho depois de um refactor
// que devia ser puramente mecanico e a rede fazendo o trabalho dela: leia o
// diff antes de rodar -update-golden. Regrave no mesmo commit da mudanca de
// comportamento que a justifica, nunca antes e nunca "para limpar".
//
// Mapa da rede, lacunas declaradas e o que cada grupo cobre:
// .corvex/tasks/rebrand/f-1-rede.md
// Anomalias congeladas de proposito: .corvex/tasks/rebrand/f-1-anomalias.md

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/activity"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// updateGolden rewrites every golden touched by the current run instead of
// comparing. Named -update-golden (not -update) so it can never be confused
// with a flag from another package's test binary.
var updateGolden = flag.Bool("update-golden", false, "rewrite cmd/testdata/golden/*.txt from the observed output")

// goldenRoot is resolved once, at package init, because runCLI* chdirs into
// temp dirs: a relative "testdata/golden" would resolve to the temp dir if a
// helper ever asserted while still inside one.
var goldenRoot = func() string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Join("testdata", "golden")
	}
	return filepath.Join(wd, "testdata", "golden")
}()

// ── in-process invocation ────────────────────────────────────────────────────

// runCLI executes the real rootCmd in-process inside a fresh, empty temp dir
// (no .corvex/, no .git/) — the right choice for "command run outside a
// project" cases and for commands that need no fixture at all.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCLIStdin(t, t.TempDir(), "", args...)
}

// runCLIIn is runCLI with an explicit working directory, normally fixture.Dir.
func runCLIIn(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCLIStdin(t, dir, "", args...)
}

// runCLIStdin is the real implementation: it chdirs to dir, feeds stdin from a
// temp file holding stdinContent (so interactive prompts read a scripted answer
// and then see a clean EOF), captures stdout/stderr, and runs rootCmd.Execute.
//
// Everything global it touches is restored on the way out: cwd, os.Stdout,
// os.Stderr, os.Stdin, charmlog's writer, and every cobra flag value.
//
// The returned err is exactly what rootCmd.Execute() returned. Note that
// cmd.Execute() in production wraps it as "Error: %s" on stderr and exits 1 —
// that wrapping is NOT reproduced here, because os.Exit would kill the test
// binary. If a golden needs to show it, render it yourself.
func runCLIStdin(t *testing.T, dir, stdinContent string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCLIExec(t, dir, stdinContent, awaitStdout{}, args...)
}

// awaitStdout asks runCLIExec to wait for stdout to carry `lines` complete lines
// before it closes the capture pipe. Zero lines means "do not wait", which is
// what every existing characterization test uses.
//
// This exists for exactly ONE caller: the drain guard in char_run_drain_test.go.
// `corvex run` prints through a renderer drained on a goroutine nobody awaits
// (cmd/run.go:171), so by default this harness closes the capture pipe the
// instant runRun returns and whatever the drain had not written yet is dropped.
// Waiting here removes the race from the TEST side — no production file is
// touched, no golden changes (renderer stdout is never in a golden, see
// runRendererPlaceholder) — so a test can state "the renderer printed X"
// deterministically instead of rolling dice with the scheduler.
//
// Measured on this machine (10 CPUs), one `run` invocation, 200 samples each:
//
//	                          lines reached stdout
//	GOMAXPROCS=1, idle                   2 / 200
//	GOMAXPROCS=1, 8 CPU hogs             0 / 200
//	GOMAXPROCS=2, idle                 199 / 200
//	GOMAXPROCS=2, 8 CPU hogs             2 / 200
//	GOMAXPROCS=10, idle              199-200 / 200
//	any of the above, with await       200 / 200
//
// The sleep in the wait loop is the mechanism: it parks the goroutine that would
// otherwise close the pipe, which is the only thing the drain was ever waiting
// for.
type awaitStdout struct {
	lines   int
	timeout time.Duration
}

// runCLIAwait is runCLIIn plus a bounded wait for `lines` lines of stdout. On
// timeout it returns whatever did arrive (possibly nothing) instead of failing,
// so the caller decides what an empty stdout means.
func runCLIAwait(t *testing.T, dir string, lines int, timeout time.Duration, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCLIExec(t, dir, "", awaitStdout{lines: lines, timeout: timeout}, args...)
}

func runCLIExec(t *testing.T, dir, stdinContent string, await awaitStdout, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	// A variadic call with no arguments yields a nil slice, and cobra falls back
	// to os.Args[1:] when args is nil — which would feed `go test` flags to the
	// CLI. Always hand it a non-nil slice.
	if args == nil {
		args = []string{}
	}

	// Read by rootCmd's PersistentPreRun and by lipgloss/charmlog: forces the
	// ASCII profile so no golden ever carries an ANSI escape.
	t.Setenv("NO_COLOR", "1")

	origWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getting cwd: %v", wdErr)
	}
	if chErr := os.Chdir(dir); chErr != nil {
		t.Fatalf("chdir %s: %v", dir, chErr)
	}
	defer func() { _ = os.Chdir(origWD) }()

	resetCLIState(t)
	defer resetCLIState(t)

	stdinFile := writeStdinFile(t, stdinContent)
	origStdin := os.Stdin
	os.Stdin = stdinFile
	defer func() {
		os.Stdin = origStdin
		_ = stdinFile.Close()
	}()

	origStdout, origStderr := os.Stdout, os.Stderr
	outR, outW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("stdout pipe: %v", pipeErr)
	}
	errR, errW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("stderr pipe: %v", pipeErr)
	}

	// Most commands print with fmt.Printf / fmt.Fprintln(os.Stderr) rather than
	// cmd.OutOrStdout(), so swapping the process handles is mandatory — setting
	// only cobra's writers would capture nothing but usage/help text.
	os.Stdout, os.Stderr = outW, errW
	// charmlog's default logger captured the real os.Stderr when it was first
	// constructed; reassigning os.Stderr does not reach it. Point it at the pipe
	// explicitly or every log.Info() line escapes to the terminal.
	charmlog.SetOutput(errW)

	// Drain concurrently: a command printing more than the pipe buffer (64KB)
	// would otherwise deadlock against itself.
	var wg sync.WaitGroup
	outBuf := &lineBuf{}
	var errBuf bytes.Buffer
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(outBuf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errBuf, errR) }()

	rootCmd.SetArgs(args)
	rootCmd.SetOut(outW)
	rootCmd.SetErr(errW)
	rootCmd.SetIn(stdinFile)

	// Named returns are filled in the defer so output survives a panic inside
	// the command under test.
	defer func() {
		// Only the drain guard sets this; see awaitStdout.
		if await.lines > 0 {
			deadline := time.Now().Add(await.timeout)
			for outBuf.Lines() < await.lines && time.Now().Before(deadline) {
				time.Sleep(200 * time.Microsecond)
			}
		}
		_ = outW.Close()
		_ = errW.Close()
		wg.Wait()
		_ = outR.Close()
		_ = errR.Close()
		os.Stdout, os.Stderr = origStdout, origStderr
		charmlog.SetOutput(origStderr)
		stdout, stderr = outBuf.String(), errBuf.String()
	}()

	err = rootCmd.Execute()
	return
}

// lineBuf is a bytes.Buffer that also counts newlines, so runCLIExec's await can
// poll "has stdout got N lines yet?" without racing the io.Copy goroutine that
// fills it (go test -race would flag a bare bytes.Buffer read here).
type lineBuf struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	lines int
}

func (b *lineBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines += bytes.Count(p, []byte{'\n'})
	return b.buf.Write(p)
}

func (b *lineBuf) Lines() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lines
}

func (b *lineBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func writeStdinFile(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing stdin file: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening stdin file: %v", err)
	}
	return f
}

// ── global state reset ───────────────────────────────────────────────────────

// resetCLIState puts the shared cobra tree back into the shape it had right
// after package init(). There is exactly one rootCmd for the whole test binary
// and every flag is bound to a package-level var, so without this a `--dry-run`
// from one test leaks into the next.
func resetCLIState(t *testing.T) {
	t.Helper()

	// root_test.go calls rootCmd.ResetFlags() and re-registers only --no-color,
	// permanently dropping --quiet from the persistent flag set. Repair both so
	// goldens (and cmd.Flags().GetBool("quiet") inside runRun) never depend on
	// test execution order.
	if rootCmd.PersistentFlags().Lookup("no-color") == nil {
		rootCmd.PersistentFlags().Bool("no-color", false, "Disable color output")
	}
	if rootCmd.PersistentFlags().Lookup("quiet") == nil {
		rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Suppress per-task progress; print only failures/errors and the final summary")
	}

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		resetFlagSet(c.Flags())
		resetFlagSet(c.PersistentFlags())
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	// The --json flags reach production code through *bool package vars. Several
	// existing tests reassign those pointers (e.g. `listJSON = &b`), which
	// silently detaches the var from the flag: after that, `list --json` prints
	// human output. Rebind both sides to one fresh bool.
	rebindJSONFlag(versionCmd, &versionJSON)
	rebindJSONFlag(listCmd, &listJSON)
	rebindJSONFlag(statusCmd, &statusJSON)
	rebindJSONFlag(inspectCmd, &inspectJSON)
	rebindJSONFlag(doctorCmd, &doctorJSON)

	rootCmd.SetArgs([]string{})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetIn(strings.NewReader(""))
}

func resetFlagSet(fs *pflag.FlagSet) {
	if fs == nil {
		return
	}
	fs.VisitAll(func(f *pflag.Flag) {
		f.Changed = false
		if f.Value == nil {
			return
		}
		// Slice-typed flags render their default as "[a,b]" and Set() appends
		// rather than replaces, so re-setting the default would corrupt them.
		// cmd/ has none today; skip defensively instead of silently appending.
		if strings.HasPrefix(f.DefValue, "[") {
			return
		}
		_ = f.Value.Set(f.DefValue)
	})
}

// testBoolValue re-implements pflag's unexported boolValue so a flag can be
// re-bound to a bool this file owns. pflag.Flag.Value is an exported field of
// an exported interface type, which makes the swap legal without touching any
// production file.
type testBoolValue struct{ p *bool }

func (b testBoolValue) String() string { return strconv.FormatBool(*b.p) }
func (b testBoolValue) Type() string   { return "bool" }
func (b testBoolValue) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	*b.p = v
	return nil
}

func rebindJSONFlag(cmd *cobra.Command, dst **bool) {
	if cmd == nil || dst == nil {
		return
	}
	f := cmd.Flags().Lookup("json")
	if f == nil {
		return
	}
	fresh := false
	*dst = &fresh
	f.Value = testBoolValue{p: &fresh}
	f.DefValue = "false"
	f.NoOptDefVal = "true" // keeps `--json` (no value) working
	f.Changed = false
}

// ── output normalisation ─────────────────────────────────────────────────────

var (
	reTSCharm  = regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}`)
	reTSRFC    = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	reCost     = regexp.MustCompile(`\$\d+(\.\d+)?`)
	reDurComp  = regexp.MustCompile(`\b\d+m\d+(\.\d+)?s\b`)
	reDur      = regexp.MustCompile(`\b\d+(\.\d+)?(ns|µs|us|ms|s|m|h)\b`)
	reHex      = regexp.MustCompile(`\b[0-9a-f]{7,64}\b`)
	reHexLower = regexp.MustCompile(`[a-f]`)
)

// scrub replaces every source of non-determinism we know about with a stable
// token. It deliberately does NOT reformat, sort or prettify anything else —
// what the CLI printed is what the golden records, bugs included.
//
// Tokens: <TMP> <HOME> <TS> <COST> <DUR> <HASH>
func scrub(s string) string {
	return scrubExcept(s)
}

// scrubExcept is scrub with some normalisers disabled by name. Use it when a
// value is actually deterministic and worth locking down — e.g. the cost
// ceilings printed by `run`'s preview come from config.yaml, so
// scrubExcept(s, "cost") keeps "$25.00/run" visible in the golden.
//
// Valid names: "tmp", "home", "ts", "cost", "dur", "hash".
func scrubExcept(s string, skip ...string) string {
	skipped := make(map[string]bool, len(skip))
	for _, k := range skip {
		skipped[k] = true
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, n := range scrubbers() {
		if skipped[n.name] {
			continue
		}
		s = n.fn(s)
	}
	return s
}

type scrubber struct {
	name string
	fn   func(string) string
}

func scrubbers() []scrubber {
	return []scrubber{
		// Paths first: a temp path can contain hex-looking segments that the
		// hash scrubber would otherwise chew up.
		{"tmp", func(s string) string {
			for _, root := range tmpRoots() {
				s = regexp.MustCompile(regexp.QuoteMeta(root)+`[^\s"':,;)\]]*`).ReplaceAllString(s, "<TMP>")
			}
			return s
		}},
		{"home", func(s string) string {
			home, err := os.UserHomeDir()
			if err != nil || home == "" || home == "/" {
				return s
			}
			return strings.ReplaceAll(s, strings.TrimRight(home, "/"), "<HOME>")
		}},
		{"ts", func(s string) string {
			s = reTSRFC.ReplaceAllString(s, "<TS>")
			return reTSCharm.ReplaceAllString(s, "<TS>")
		}},
		// Cost before duration: "$25.00" must not be half-eaten by the "s"/"m"
		// duration pattern.
		{"cost", func(s string) string { return reCost.ReplaceAllString(s, "<COST>") }},
		{"dur", func(s string) string {
			s = reDurComp.ReplaceAllString(s, "<DUR>")
			return reDur.ReplaceAllString(s, "<DUR>")
		}},
		{"hash", func(s string) string {
			return reHex.ReplaceAllStringFunc(s, func(m string) string {
				// Require at least one a-f so plain 7+ digit numbers (token
				// counts, ports, durations in ms) survive untouched.
				if !reHexLower.MatchString(m) {
					return m
				}
				return "<HASH>"
			})
		}},
	}
}

// tmpRoots lists every prefix a t.TempDir() path can appear under, longest
// first. macOS is the awkward one: os.TempDir() reports /var/folders/... while
// resolved paths surface as /private/var/folders/...
func tmpRoots() []string {
	seen := map[string]bool{}
	var roots []string
	add := func(p string) {
		p = strings.TrimRight(p, "/")
		if p == "" || p == "/" || seen[p] {
			return
		}
		seen[p] = true
		roots = append(roots, p)
	}
	if td := os.TempDir(); td != "" {
		add(td)
		add("/private" + td)
		if resolved, err := filepath.EvalSymlinks(td); err == nil {
			add(resolved)
		}
	}
	add("/private/var/folders")
	add("/var/folders")
	add("/private/tmp")
	add("/tmp")
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) > len(roots[j]) })
	return roots
}

// scrubPath replaces one specific directory (and its symlink-resolved form)
// with <TMP>. Only needed when a path lives outside the standard temp roots —
// scrub() already covers t.TempDir().
func scrubPath(s, dir string) string {
	dir = strings.TrimRight(dir, "/")
	if dir == "" || dir == "/" {
		return s
	}
	s = strings.ReplaceAll(s, dir, "<TMP>")
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		s = strings.ReplaceAll(s, strings.TrimRight(resolved, "/"), "<TMP>")
	}
	return s
}

// ── transcript + golden ──────────────────────────────────────────────────────

// transcript renders one invocation as the canonical golden body: the argv, the
// two streams, and the returned error. Keeping all three in one file is what
// makes a golden fail when output silently moves from stdout to stderr.
func transcript(args []string, stdout, stderr string, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$ corvex %s\n", strings.Join(args, " "))
	b.WriteString("\n--- stdout ---\n")
	b.WriteString(streamBlock(stdout))
	b.WriteString("\n--- stderr ---\n")
	b.WriteString(streamBlock(stderr))
	b.WriteString("\n--- error ---\n")
	if err == nil {
		b.WriteString("(nil)\n")
	} else {
		fmt.Fprintf(&b, "%s\n", err)
	}
	return b.String()
}

func streamBlock(s string) string {
	if s == "" {
		return "(empty)\n"
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// goldenAssert compares got against cmd/testdata/golden/<name>.txt, or rewrites
// that file when -update-golden is passed. Missing golden is a hard failure
// with the command to create it — never a silent pass.
func goldenAssert(t *testing.T, name, got string) {
	t.Helper()

	if !strings.HasSuffix(got, "\n") {
		got += "\n"
	}
	path := filepath.Join(goldenRoot, name+".txt")

	if *updateGolden {
		if err := os.MkdirAll(goldenRoot, 0o755); err != nil {
			t.Fatalf("creating %s: %v", goldenRoot, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("golden updated: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s not readable (%v)\ncreate it with:\n  go test ./cmd/ -run %s -update-golden\n--- observed ---\n%s",
			path, err, t.Name(), got)
	}
	if string(want) == got {
		return
	}
	t.Errorf("golden %s mismatch\n%s", path, goldenDiff(string(want), got))
}

// goldenDiff reports the first differing line plus both full bodies. A real
// diff library would be nicer; the first-mismatch pointer is what actually
// makes a failure readable in practice.
func goldenDiff(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(wl) || i < len(gl); i++ {
		w, g := "", ""
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			fmt.Fprintf(&b, "first mismatch at line %d:\n  want: %q\n  got:  %q\n", i+1, w, g)
			break
		}
	}
	fmt.Fprintf(&b, "\n--- want (%d lines) ---\n%s\n--- got (%d lines) ---\n%s", len(wl), want, len(gl), got)
	return b.String()
}

// ── project fixture ──────────────────────────────────────────────────────────

// fixtureConfigYAML is the canonical .corvex/config.yaml: a valid provider,
// all three models set, local sandbox and explicit cost ceilings, so the only
// doctor checks that can move are the ones a test deliberately controls.
const fixtureConfigYAML = `project:
  name: fixture
  description: characterization fixture
provider:
  default: claude-cli
  models:
    planner: opus
    worker: sonnet
    reviewer: sonnet
sandbox:
  type: local
execution:
  max_retries: 2
  auto_commit: false
  max_cost_usd: 25
  max_cost_per_task_usd: 5
`

// fixtureSpecMD is a minimal spec.md — enough for a project to be "listed" and
// for anchor.SpecHash to have something to hash.
const fixtureSpecMD = "# fixture\n\n## Objective\n\nCharacterize the CLI.\n"

// fixtureTasksMD is a two-task DAG (S01 PASSED, S02 PENDING depends on S01) in
// the exact shape internal/task.ParseTasksFile accepts. Copied from the format
// already proven by json_fixture_test.go — do not "tidy" it, the parser is
// picky about the heading/emoji/section layout.
const fixtureTasksMD = "---\ngenerated_by: characterize\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
	"## S01 — First Task ✅ PASSED\n\n" +
	"```yaml\ntype: general\n```\n\n" +
	"### O que fazer\nFirst task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n\n---\n\n" +
	"## S02 — Second Task ⬜ PENDING\n\n" +
	"```yaml\ntype: backend\ndepends_on: [S01]\n```\n\n" +
	"### O que fazer\nSecond task\n\n" +
	"### Critérios de sucesso\n- [ ] Done\n"

// fixtureLedgerTime is a frozen clock for ledger entries. Entry timestamps are
// not printed today, but pinning them keeps activity.jsonl byte-identical if a
// future command starts showing them.
var fixtureLedgerTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fixture is a temp directory shaped like a repo with a Corvex project in it.
// Methods chain: newFixture(t).AddProject(...).GitInit().
type fixture struct {
	t *testing.T
	// Dir is the repo root: it holds .corvex/ and, after GitInit, .git/.
	// Pass it to runCLIIn.
	Dir string
}

// newFixture creates a temp dir containing .corvex/config.yaml and an empty
// .corvex/tasks/. No project yet — commands that require one will error, which
// is often exactly the behaviour worth characterizing.
//
// Deliberately writes no .env / .corvex/*.env: config.Load() sources those into
// the process environment, which would leak between tests.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, Dir: t.TempDir()}
	f.Write(filepath.Join(".corvex", "config.yaml"), fixtureConfigYAML)
	f.Mkdir(filepath.Join(".corvex", "tasks"))
	return f
}

// newBareFixture creates a temp dir with .corvex/ but NO config.yaml, so
// ops.LoadConfig() falls back to config.Default(). Useful for characterizing what
// happens with a project but no configuration.
func newBareFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, Dir: t.TempDir()}
	f.Mkdir(filepath.Join(".corvex", "tasks"))
	return f
}

// AddProject creates .corvex/tasks/<name>/ with spec.md and tasks.md. Pass ""
// for either body to leave that file out (e.g. spec only = "needs planning").
func (f *fixture) AddProject(name, spec, tasks string) *fixture {
	f.t.Helper()
	f.Mkdir(filepath.Join(".corvex", "tasks", name))
	if spec != "" {
		f.Write(filepath.Join(".corvex", "tasks", name, "spec.md"), spec)
	}
	if tasks != "" {
		f.Write(filepath.Join(".corvex", "tasks", name, "tasks.md"), tasks)
	}
	return f
}

// AddLedger appends entries to .corvex/tasks/<project>/activity.jsonl. Entries
// with a zero Timestamp get fixtureLedgerTime so the file stays deterministic.
// The project directory must already exist (activity.New stats it).
func (f *fixture) AddLedger(project string, entries ...activity.Entry) *fixture {
	f.t.Helper()
	ledger, err := activity.New(f.Dir, project)
	if err != nil {
		f.t.Fatalf("creating ledger for %s: %v", project, err)
	}
	for _, e := range entries {
		if e.Timestamp.IsZero() {
			e.Timestamp = fixtureLedgerTime
		}
		if err := ledger.Append(e); err != nil {
			f.t.Fatalf("appending ledger entry %+v: %v", e, err)
		}
	}
	return f
}

// GitInit turns the fixture into a real git repo with one empty commit —
// required by anything that calls findGitRoot (start, and the worktree-mismatch
// guard in run/plan). Reuses gitInit from start_test.go, which already isolates
// the global/system git config.
func (f *fixture) GitInit() *fixture {
	f.t.Helper()
	gitInit(f.t, f.Dir)
	return f
}

// Write creates a file at a fixture-relative path, making parent dirs.
func (f *fixture) Write(rel, content string) *fixture {
	f.t.Helper()
	path := filepath.Join(f.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatalf("writing %s: %v", rel, err)
	}
	return f
}

// Mkdir creates a fixture-relative directory.
func (f *fixture) Mkdir(rel string) *fixture {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.Dir, rel), 0o755); err != nil {
		f.t.Fatalf("mkdir %s: %v", rel, err)
	}
	return f
}

// Path joins fixture-relative parts into an absolute path.
func (f *fixture) Path(parts ...string) string {
	return filepath.Join(append([]string{f.Dir}, parts...)...)
}

// Read returns the contents of a fixture-relative file (for asserting that a
// command wrote what it claimed to write).
func (f *fixture) Read(rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Dir, rel))
	if err != nil {
		f.t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

// ── stub binaries ────────────────────────────────────────────────────────────

// stubBin writes an executable /bin/sh script named `name` into a fresh temp
// dir and prepends that dir to PATH for the remainder of the test. Use it for
// commands that shell out (git, docker) when the real binary would be slow,
// absent, or destructive. Returns the stub's absolute path.
//
// script is the body after the shebang, e.g.
//
//	stubBin(t, "docker", `echo "docker $@"; exit 0`)
func stubBin(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("writing stub %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

// stubClaude points CORVEX_CLAUDE_BIN at a stub script, which is the ONLY
// reliable way to keep provider paths (plan, grill, validate, run) from
// invoking the real Claude CLI and spending money. provider.NewProvider never
// checks PATH, so the fake must be installed before the command runs.
//
// Pass a script that exits non-zero to characterize the failure path:
//
//	stubClaude(t, `echo "stub: refusing" >&2; exit 1`)
func stubClaude(t *testing.T, script string) string {
	t.Helper()
	path := stubBin(t, "claude", script)
	t.Setenv("CORVEX_CLAUDE_BIN", path)
	return path
}

// ── harness self-tests ───────────────────────────────────────────────────────
//
// These three prove the harness works end to end: process-level stdout capture
// (version), cobra's own writer (help), and the fixture + cwd plumbing (list).
// Their goldens are namespaced "harness_*"; name yours after the command
// (e.g. "list_json", "run_dryrun") so files never collide.

func TestCharacterizeVersion(t *testing.T) {
	args := []string{"version"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "harness_version", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeRootHelp(t *testing.T) {
	args := []string{"--help"}
	stdout, stderr, err := runCLI(t, args...)
	goldenAssert(t, "harness_root_help", scrub(transcript(args, stdout, stderr, err)))
}

func TestCharacterizeFixtureList(t *testing.T) {
	f := newFixture(t).
		AddProject("alpha", fixtureSpecMD, fixtureTasksMD).
		AddProject("beta", fixtureSpecMD, "")

	args := []string{"list"}
	stdout, stderr, err := runCLIIn(t, f.Dir, args...)
	goldenAssert(t, "harness_fixture_list", scrub(transcript(args, stdout, stderr, err)))
}
