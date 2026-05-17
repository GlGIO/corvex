package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/orchestrator"
	"github.com/giovannialves/corvex/internal/provider"
	"gopkg.in/yaml.v3"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate <project>",
	Short: "Run integration validation against the live stack",
	Long: `Validate spins up the configured stack (DB + app + optional Chrome),
then runs an AI agent to test endpoints and UI flows against the spec.`,
	Args: cobra.ExactArgs(1),
	RunE: runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func runValidate(cmd *cobra.Command, args []string) error {
	project := args[0]

	cfg, workDir, err := loadConfig()
	if err != nil {
		return err
	}
	if err := requireCorvexDir(workDir); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	if !validateConfigured(cfg.Validate) {
		if err := runValidateWizard(cmd.Context(), reader, workDir, cfg); err != nil {
			return fmt.Errorf("validate wizard: %w", err)
		}
		cfg, workDir, err = loadConfig()
		if err != nil {
			return err
		}
	}

	return validateProject(cmd.Context(), cfg, workDir, project)
}

// validateProject is the shared entry point used by both validateCmd and --validate in run.
func validateProject(ctx context.Context, cfg *config.Config, workDir, project string) error {
	pDir := projectDir(workDir, project)
	specPath := filepath.Join(pDir, "spec.md")
	tasksPath := filepath.Join(pDir, "tasks.md")

	log.Info("setting up validation stack", "project", project)
	cleanup, err := setupValidationStack(ctx, workDir, project, cfg.Validate)
	if err != nil {
		return fmt.Errorf("stack setup failed: %w", err)
	}
	defer cleanup()

	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	validator := orchestrator.NewValidator(p, cfg.Provider.Models.Worker, workDir)

	log.Info("running validator agent", "project", project)
	result, err := validator.Validate(ctx, specPath, tasksPath, cfg.Validate)
	if err != nil {
		return fmt.Errorf("validator: %w", err)
	}

	fmt.Printf("\n%s\n", result.Summary)

	if result.Verdict == orchestrator.VerdictPass {
		fmt.Printf("\n✓ PASS  ($%.2f, %ds)\n", result.CostUSD, result.DurationMs/1000)
		return nil
	}

	fmt.Printf("\n✗ FAIL  ($%.2f, %ds)\n", result.CostUSD, result.DurationMs/1000)
	return fmt.Errorf("validation failed")
}

// ── Config wizard ─────────────────────────────────────────────────────────────

func validateConfigured(v config.ValidateConfig) bool {
	return v.Stack.Port != 0 || v.Stack.StartCommand != ""
}

func runValidateWizard(ctx context.Context, reader *bufio.Reader, workDir string, cfg *config.Config) error {
	fmt.Println("\nvalidate: not configured.")
	fmt.Println("Inspecting codebase to draft a config (one AI call)...")
	fmt.Println()

	draft, err := inferValidateConfig(ctx, cfg, workDir)
	if err != nil {
		log.Warn("AI inference failed — falling back to manual wizard", "err", err)
		return manualValidateWizard(reader, workDir, cfg)
	}

	printDetected(draft.Detected)

	cfg.Validate = draft.Validate
	confirmUncertain(reader, &cfg.Validate, draft.Uncertain)

	fmt.Printf("\nEdit any field manually? (y/n) [n]: ")
	if raw, _ := reader.ReadString('\n'); strings.TrimSpace(raw) == "y" {
		manualOverride(reader, &cfg.Validate)
	}

	if err := saveConfig(workDir, cfg); err != nil {
		return err
	}
	fmt.Printf("\n✓ Written to .corvex/config.yaml  (inference cost: $%.4f)\n\n", draft.CostUSD)
	return nil
}

func inferValidateConfig(ctx context.Context, cfg *config.Config, workDir string) (*orchestrator.ConfigDraft, error) {
	p, err := provider.NewProvider(cfg.Provider.Default, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider: %w", err)
	}
	c := orchestrator.NewConfigurer(p, cfg.Provider.Models.Planner, workDir)
	return c.InferValidate(ctx)
}

func printDetected(detected []orchestrator.DetectedField) {
	if len(detected) == 0 {
		fmt.Println("(nothing auto-detected — please confirm/enter values manually)")
		return
	}
	fmt.Println("✓ Detected:")
	for _, d := range detected {
		if d.Source != "" {
			fmt.Printf("  %s = %s  (from %s)\n", d.Field, d.Value, d.Source)
		} else {
			fmt.Printf("  %s = %s\n", d.Field, d.Value)
		}
	}
}

func confirmUncertain(reader *bufio.Reader, v *config.ValidateConfig, uncertain []orchestrator.UncertainField) {
	if len(uncertain) == 0 {
		return
	}
	fmt.Println("\n? Please confirm uncertain fields:")
	for _, u := range uncertain {
		hint := u.Guess
		if u.Reason != "" {
			hint = fmt.Sprintf("%s — %s", u.Guess, u.Reason)
		}
		answer := wizardPrompt(reader, "  "+u.Field+" ("+hint+")", u.Guess)
		applyFieldOverride(v, u.Field, answer)
	}
}

func manualOverride(reader *bufio.Reader, v *config.ValidateConfig) {
	fmt.Println("\nManual edit — Enter to keep current value:")
	v.Stack.Runtime = wizardPrompt(reader, "  stack.runtime", v.Stack.Runtime)
	v.Stack.Framework = wizardPrompt(reader, "  stack.framework", v.Stack.Framework)
	v.Stack.StartCommand = wizardPrompt(reader, "  stack.start_command", v.Stack.StartCommand)
	v.Stack.Port = parseIntDefault(wizardPrompt(reader, "  stack.port", strconv.Itoa(v.Stack.Port)), v.Stack.Port)
	v.Stack.HealthPath = wizardPrompt(reader, "  stack.health_path", v.Stack.HealthPath)
	v.Database.Type = wizardPrompt(reader, "  database.type", v.Database.Type)
	v.Database.Image = wizardPrompt(reader, "  database.image", v.Database.Image)
	v.Database.MigrateCommand = wizardPrompt(reader, "  database.migrate_command", v.Database.MigrateCommand)
	uiAnswer := wizardPrompt(reader, "  ui.enabled (y/n)", boolStr(v.UI.Enabled))
	v.UI.Enabled = uiAnswer == "y" || uiAnswer == "yes" || uiAnswer == "true"
}

// applyFieldOverride writes value to v at the given dotted path (e.g. "stack.port").
func applyFieldOverride(v *config.ValidateConfig, field, value string) {
	switch field {
	case "stack.runtime":
		v.Stack.Runtime = value
	case "stack.framework":
		v.Stack.Framework = value
	case "stack.start_command":
		v.Stack.StartCommand = value
	case "stack.port":
		v.Stack.Port = parseIntDefault(value, v.Stack.Port)
	case "stack.health_path":
		v.Stack.HealthPath = value
	case "stack.ready_timeout":
		v.Stack.ReadyTimeout = parseIntDefault(value, v.Stack.ReadyTimeout)
	case "database.type":
		v.Database.Type = value
	case "database.image":
		v.Database.Image = value
	case "database.migrate_command":
		v.Database.MigrateCommand = value
	case "ui.enabled":
		v.UI.Enabled = value == "y" || value == "yes" || value == "true"
	}
}

func parseIntDefault(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}

func boolStr(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

// manualValidateWizard is the fallback when AI inference is unavailable.
func manualValidateWizard(reader *bufio.Reader, workDir string, cfg *config.Config) error {
	cfg.Validate.Stack.Runtime = wizardPrompt(reader, "Backend runtime? (node/python/go/java)", "node")
	cfg.Validate.Stack.Framework = wizardPrompt(reader, "Framework? (nestjs/express/fastapi/...)", "")
	cfg.Validate.Stack.StartCommand = wizardPrompt(reader, "Start command?", "npm run start:test")
	cfg.Validate.Stack.Port = parseIntDefault(wizardPrompt(reader, "Port?", "3000"), 3000)
	cfg.Validate.Stack.ReadyTimeout = 30
	cfg.Validate.Stack.HealthPath = wizardPrompt(reader, "Health check path? (empty to skip)", "/health")

	dbType := wizardPrompt(reader, "Database? (postgres/mysql/sqlite/none)", "postgres")
	cfg.Validate.Database.Type = dbType
	if dbType != "none" && dbType != "sqlite" && dbType != "" {
		defaultImage := dbType + ":latest"
		if dbType == "postgres" {
			defaultImage = "postgres:16"
		} else if dbType == "mysql" {
			defaultImage = "mysql:8"
		}
		cfg.Validate.Database.Image = wizardPrompt(reader, "DB docker image?", defaultImage)
		cfg.Validate.Database.MigrateCommand = wizardPrompt(reader, "Migrate command?", "npm run migrate")
		if dbType == "postgres" {
			cfg.Validate.Database.Env = map[string]string{
				"POSTGRES_DB":       "testdb",
				"POSTGRES_USER":     "test",
				"POSTGRES_PASSWORD": "test",
			}
		}
	}

	uiAnswer := wizardPrompt(reader, "Test UI with Chrome CDP? (y/n)", "n")
	cfg.Validate.UI.Enabled = uiAnswer == "y" || uiAnswer == "yes"

	if err := saveConfig(workDir, cfg); err != nil {
		return err
	}
	fmt.Printf("\n✓ Written to .corvex/config.yaml\n\n")
	return nil
}

func wizardPrompt(reader *bufio.Reader, question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", question, defaultVal)
	} else {
		fmt.Printf("  %s: ", question)
	}
	raw, err := reader.ReadString('\n')
	if err != nil {
		return defaultVal
	}
	if answer := strings.TrimSpace(raw); answer != "" {
		return answer
	}
	return defaultVal
}

func saveConfig(workDir string, cfg *config.Config) error {
	configPath := filepath.Join(workDir, ".corvex", "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(configPath, data, 0o644)
}

// ── Stack setup / teardown ────────────────────────────────────────────────────

type cleanupFn func()

func setupValidationStack(ctx context.Context, workDir, project string, cfg config.ValidateConfig) (cleanupFn, error) {
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 1. Database container
	if cfg.Database.Type != "" && cfg.Database.Type != "none" && cfg.Database.Type != "sqlite" {
		dbCleanup, err := startDBContainer(ctx, project, cfg.Database)
		if err != nil {
			cleanup()
			return nil, err
		}
		cleanups = append(cleanups, dbCleanup)
		log.Info("database ready", "type", cfg.Database.Type)
	}

	// 2. Migrations
	if cfg.Database.MigrateCommand != "" {
		log.Info("running migrations")
		parts := strings.Fields(cfg.Database.MigrateCommand)
		c := exec.CommandContext(ctx, parts[0], parts[1:]...)
		c.Dir = workDir
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			cleanup()
			return nil, fmt.Errorf("migrations failed: %w", err)
		}
	}

	// 3. Application
	appCmd, err := startApp(workDir, cfg.Stack)
	if err != nil {
		cleanup()
		return nil, err
	}
	cleanups = append(cleanups, func() {
		if appCmd.Process != nil {
			appCmd.Process.Kill()
			appCmd.Wait()
		}
	})

	// 4. Wait for app health
	log.Info("waiting for app to be ready", "port", cfg.Stack.Port)
	if err := waitForHealth(ctx, cfg.Stack); err != nil {
		cleanup()
		return nil, err
	}
	log.Info("app ready")

	// 5. Chrome (if UI enabled)
	if cfg.UI.Enabled {
		chromeCmd, err := startChrome(ctx)
		if err != nil {
			cleanup()
			return nil, err
		}
		cleanups = append(cleanups, func() {
			if chromeCmd.Process != nil {
				chromeCmd.Process.Kill()
				chromeCmd.Wait()
			}
		})
		log.Info("chrome CDP ready", "port", 9222)
	}

	return cleanup, nil
}

func startDBContainer(ctx context.Context, project string, cfg config.ValidateDBConfig) (func(), error) {
	name := "corvex-val-" + project + "-db"

	// Remove any leftover container from a previous run.
	exec.Command("docker", "rm", "-f", name).Run()

	args := []string{"run", "-d", "--rm", "--name", name}
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, cfg.Image)

	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("starting %s container: %s: %w", cfg.Type, strings.TrimSpace(string(out)), err)
	}

	stopFn := func() { exec.Command("docker", "rm", "-f", name).Run() }

	// Poll until ready.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var check *exec.Cmd
		switch cfg.Type {
		case "postgres":
			check = exec.Command("docker", "exec", name, "pg_isready")
		case "mysql":
			check = exec.Command("docker", "exec", name, "mysqladmin", "ping", "--silent")
		default:
			return stopFn, nil
		}
		if check.Run() == nil {
			return stopFn, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	stopFn()
	return nil, fmt.Errorf("database did not become ready within 60s")
}

func startApp(workDir string, cfg config.ValidateStackConfig) (*exec.Cmd, error) {
	parts := strings.Fields(cfg.StartCommand)
	if len(parts) == 0 {
		return nil, fmt.Errorf("start_command is empty")
	}
	c := exec.Command(parts[0], parts[1:]...)
	c.Dir = workDir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("starting app (%s): %w", cfg.StartCommand, err)
	}
	return c, nil
}

func waitForHealth(ctx context.Context, cfg config.ValidateStackConfig) error {
	path := cfg.HealthPath
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://localhost:%d%s", cfg.Port, path)

	timeout := time.Duration(cfg.ReadyTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("app did not become ready at %s within %v", url, timeout)
}

func startChrome(ctx context.Context) (*exec.Cmd, error) {
	binary := ""
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if _, err := exec.LookPath(candidate); err == nil {
			binary = candidate
			break
		}
	}
	if binary == "" {
		return nil, fmt.Errorf("no Chrome/Chromium binary found — install chromium or google-chrome")
	}

	c := exec.CommandContext(ctx, binary,
		"--headless",
		"--remote-debugging-port=9222",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
	)
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("starting Chrome (%s): %w", binary, err)
	}

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://localhost:9222/json/version")
		if err == nil {
			resp.Body.Close()
			return c, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	c.Process.Kill()
	return nil, fmt.Errorf("Chrome CDP did not become ready on port 9222")
}
