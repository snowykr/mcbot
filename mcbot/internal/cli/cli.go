package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/snowy/mcbot/internal/botapp"
	"github.com/snowy/mcbot/internal/composectl"
	"github.com/snowy/mcbot/internal/envfile"
	"github.com/snowy/mcbot/internal/mcconfig"
	"github.com/snowy/mcbot/internal/serverops"
)

const (
	ExitOK          = 0
	ExitInternal    = 1
	ExitUsage       = 2
	ExitValidation  = 3
	ExitOperational = 4
)

const version = "dev"

type Command struct {
	Name        string
	Description string
	Subcommands []Command
}

type Options struct {
	JSON        bool
	Quiet       bool
	NoInput     bool
	Yes         bool
	Force       bool
	ShowSecrets bool
}

type ErrorKind int

const (
	ErrorKindInternal ErrorKind = iota
	ErrorKindUsage
	ErrorKindValidation
	ErrorKindOperational
)

type CLIError struct {
	Kind ErrorKind
	Err  error
}

func (e *CLIError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Output struct {
	stdout io.Writer
	stderr io.Writer
	quiet  bool
	style  Style
	state  *outputState
}

type outputState struct {
	currentSetupStep  string
	currentSetupTitle string
}

var runBot = botapp.Run
var serverRunner serverOperations = defaultServerOperations{}
var configPathOverride string
var envPathOverride string

type serverOperations interface {
	Start(ctx context.Context) (serverops.Result, error)
	Stop(ctx context.Context) (serverops.Result, error)
	Status(ctx context.Context) (serverops.Result, error)
}

type defaultServerOperations struct{}

func (defaultServerOperations) Start(ctx context.Context) (serverops.Result, error) {
	return serverops.Start(ctx, serverops.Options{})
}

func (defaultServerOperations) Stop(ctx context.Context) (serverops.Result, error) {
	return serverops.Stop(ctx, serverops.Options{})
}

func (defaultServerOperations) Status(ctx context.Context) (serverops.Result, error) {
	return serverops.Status(ctx, serverops.Options{})
}

func SetBotRunnerForTest(runner func() error) func() {
	oldRunner := runBot
	runBot = runner
	return func() {
		runBot = oldRunner
	}
}

func SetServerRunnerForTest(runner serverOperations) func() {
	oldRunner := serverRunner
	serverRunner = runner
	return func() {
		serverRunner = oldRunner
	}
}

func SetConfigPathForTest(path string) func() {
	oldPath := configPathOverride
	configPathOverride = path
	return func() {
		configPathOverride = oldPath
	}
}

func SetEnvPathForTest(path string) func() {
	oldPath := envPathOverride
	envPathOverride = path
	return func() {
		envPathOverride = oldPath
	}
}

func Run(args []string, stdout, stderr io.Writer) int {
	return runWithIO(args, os.Stdin, stdout, stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, remaining, exitCode := parseGlobalFlags(args, stdout, stderr)
	if exitCode >= 0 {
		return exitCode
	}
	if exitCode := validateGlobalFlagContract(opts, remaining, stderr); exitCode >= 0 {
		return exitCode
	}

	if len(remaining) == 0 {
		printRootHelp(stdout)
		return ExitOK
	}

	switch remaining[0] {
	case "help":
		printRootHelp(stdout)
		return ExitOK
	case "version":
		fmt.Fprintf(stdout, "mcbot %s\n", version)
		return ExitOK
	case "bot", "server", "config", "env", "setup":
		return dispatchPlaceholder(opts, remaining, stdin, stdout, stderr)
	default:
		return usageError(stderr, "unknown command %q", remaining[0])
	}
}

func NewOutput(stdout, stderr io.Writer, opts Options) Output {
	return Output{stdout: stdout, stderr: stderr, quiet: opts.Quiet, style: NewStyle(stdout), state: &outputState{}}
}

func (o Output) SuccessText(format string, args ...any) error {
	if o.quiet {
		return nil
	}
	_, err := fmt.Fprintf(o.stdout, format, args...)
	return err
}

func (o Output) SuccessLine(format string, args ...any) error {
	if o.quiet {
		return nil
	}
	_, err := fmt.Fprintf(o.stdout, format+"\n", args...)
	return err
}

func (o Output) SetupHeader(title, subtitle string) error {
	if o.quiet {
		return nil
	}
	if o.style.enabled {
		if _, err := fmt.Fprint(o.stdout, "[2J[H"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(o.stdout, o.style.Accent("╭──────────────────────────────────────╮")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(o.stdout, o.setupBoxLine(title, o.style.Header)); err != nil {
		return err
	}
	if subtitle != "" {
		if _, err := fmt.Fprintln(o.stdout, o.setupBoxLine(subtitle, o.style.Dim)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(o.stdout, o.style.Accent("╰──────────────────────────────────────╯"))
	return err
}

func (o Output) setupBoxLine(text string, textStyle func(string) string) string {
	return o.setupBorderedLine("  " + textStyle(setupBoxText(text)) + " ")
}

func setupBoxText(text string) string {
	return fmt.Sprintf("%-35s", text)
}

func (o Output) setupReviewLine(inner string) string {
	return o.setupBorderedLine(fmt.Sprintf("%-38s", inner))
}

func (o Output) setupBorderedLine(inner string) string {
	return o.style.Accent("│") + inner + o.style.Accent("│")
}

func (o Output) SetupIntro(envPath, configPath string) error {
	if o.quiet {
		return nil
	}
	if err := o.SetupHeader("MCBot Setup", "Discord + Minecraft server wizard"); err != nil {
		return err
	}
	lines := []string{
		"",
		"This wizard will create/update:",
		"  • " + setupReviewPath(envPath),
		"  • " + setupReviewPath(configPath),
		"",
		"Steps:",
		"  1. Discord bot",
		"  2. Runtime features",
		"  3. Minecraft server",
		"  4. Container behavior",
		"  5. Review & write",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(o.stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func (o Output) SetupSection(index, total int, title string) error {
	if o.quiet {
		return nil
	}
	if o.style.enabled {
		if _, err := fmt.Fprint(o.stdout, "\x1b[2J\x1b[H"); err != nil {
			return err
		}
	}
	step := fmt.Sprintf("[%d/%d] %s", index, total, title)
	if o.state != nil {
		o.state.currentSetupStep = step
		o.state.currentSetupTitle = title
	}
	_, err := fmt.Fprintf(o.stdout, "\n%s\n%s\n", o.style.Dim("────────────────────────────────────────"), o.style.Accent(step))
	return err
}

func (o Output) SetupQuestion(section string, title string, help ...string) error {
	if o.quiet {
		return nil
	}
	if o.style.enabled {
		if _, err := fmt.Fprint(o.stdout, "\x1b[2J\x1b[H"); err != nil {
			return err
		}
	}
	if o.style.enabled && o.state != nil && o.state.currentSetupStep != "" {
		if _, err := fmt.Fprintf(o.stdout, "%s\n", o.style.Accent(o.state.currentSetupStep)); err != nil {
			return err
		}
	}
	if section != "" {
		if o.state != nil && section == o.state.currentSetupTitle {
			if _, err := fmt.Fprintln(o.stdout); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(o.stdout, "%s\n", o.style.Dim(section)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(o.stdout, "%s\n", o.style.Accent(title)); err != nil {
		return err
	}
	for _, line := range help {
		if line == "" {
			continue
		}
		if err := o.SetupHint(line); err != nil {
			return err
		}
	}
	return nil
}

func (o Output) SetupHint(text string) error {
	if o.quiet {
		return nil
	}
	_, err := fmt.Fprintf(o.stdout, "%s\n", text)
	return err
}

func (o Output) SetupReview(envPath string, entries []envfile.Entry, configPath string, cfg mcconfig.Config) error {
	if o.quiet {
		return nil
	}
	if o.style.enabled {
		if _, err := fmt.Fprint(o.stdout, "\x1b[2J\x1b[H"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(o.stdout, "\n%s\n", o.style.Accent("╭─ Review ─────────────────────────────╮")); err != nil {
		return err
	}
	if envPath != "" {
		if _, err := fmt.Fprintln(o.stdout, o.setupReviewLine(" "+setupReviewPath(envPath)+" ")); err != nil {
			return err
		}
		for _, key := range []string{"DISCORD_TOKEN", "MCBOT_TRUSTED_GUILD_ID", "MCBOT_DEBUG", "ENABLE_RCON"} {
			value := setupEnvReviewValue(entries, key)
			if value == "" {
				continue
			}
			if key == "DISCORD_TOKEN" && value == envfile.MaskedValue {
				value = "set, masked"
			}
			if _, err := fmt.Fprintln(o.stdout, o.setupReviewLine(fmt.Sprintf("  %-22s %-12s ", key, truncateReviewValue(value, 12)))); err != nil {
				return err
			}
		}
		if configPath != "" {
			if _, err := fmt.Fprintln(o.stdout, o.setupReviewLine("")); err != nil {
				return err
			}
		}
	}
	if configPath != "" {
		if _, err := fmt.Fprintln(o.stdout, o.setupReviewLine(" "+setupReviewPath(configPath)+" ")); err != nil {
			return err
		}
		rows := [][2]string{
			{"server.type", cfg.Server.Type},
			{"server.memory", cfg.Server.Memory},
			{"restart_policy", cfg.Container.RestartPolicy},
			{"uid/gid", fmt.Sprintf("%d:%d", cfg.Container.UID, cfg.Container.GID)},
		}
		for _, row := range rows {
			if _, err := fmt.Fprintln(o.stdout, o.setupReviewLine(fmt.Sprintf("  %-22s %-12s ", row[0], truncateReviewValue(row[1], 12)))); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(o.stdout, "%s\n", o.style.Accent("╰──────────────────────────────────────╯"))
	return err
}

func setupReviewPath(path string) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return truncateReviewValue(path, 36)
	}
	return truncateReviewValue(base, 36)
}

func (o Output) SetupComplete(envPath, configPath string) error {
	if o.quiet {
		return nil
	}
	if envPath != "" {
		if _, err := fmt.Fprintln(o.stdout, "✓ Wrote "+envPath); err != nil {
			return err
		}
	}
	if configPath != "" {
		if _, err := fmt.Fprintln(o.stdout, "✓ Wrote "+configPath); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(o.stdout, "✓ Validation passed"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(o.stdout, ""); err != nil {
		return err
	}
	if configPath != "" {
		if _, err := fmt.Fprintln(o.stdout, "Note: restart policy, port, UID/GID changes may require recreating an existing mc-server container."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(o.stdout, ""); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(o.stdout, "Next:"); err != nil {
		return err
	}
	if envPath != "" {
		if _, err := fmt.Fprintln(o.stdout, "make env-validate"); err != nil {
			return err
		}
	}
	if configPath != "" {
		if _, err := fmt.Fprintln(o.stdout, "make config-validate"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(o.stdout, "make up-all"); err != nil {
			return err
		}
	}
	return nil
}

func setupEnvReviewValue(entries []envfile.Entry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Value
		}
	}
	return ""
}

func truncateReviewValue(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 1 {
		return value[:width]
	}
	return value[:width-1] + "…"
}

func (o Output) SuccessJSON(payload any) error {
	encoder := json.NewEncoder(o.stdout)
	return encoder.Encode(payload)
}

func (o Output) Diagnostic(format string, args ...any) error {
	_, err := fmt.Fprintf(o.stderr, format+"\n", args...)
	return err
}

func ExitCodeForError(err error) int {
	if err == nil {
		return ExitOK
	}

	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		switch cliErr.Kind {
		case ErrorKindUsage:
			return ExitUsage
		case ErrorKindValidation:
			return ExitValidation
		case ErrorKindOperational:
			return ExitOperational
		case ErrorKindInternal:
			return ExitInternal
		}
	}

	return ExitInternal
}

func UsageError(format string, args ...any) error {
	return &CLIError{Kind: ErrorKindUsage, Err: fmt.Errorf(format, args...)}
}

func ValidationError(format string, args ...any) error {
	return &CLIError{Kind: ErrorKindValidation, Err: fmt.Errorf(format, args...)}
}

func OperationalError(format string, args ...any) error {
	return &CLIError{Kind: ErrorKindOperational, Err: fmt.Errorf(format, args...)}
}

func InternalError(format string, args ...any) error {
	return &CLIError{Kind: ErrorKindInternal, Err: fmt.Errorf(format, args...)}
}

func CommandTree() []Command {
	return []Command{
		{Name: "help", Description: "Show help"},
		{Name: "version", Description: "Print version"},
		{Name: "bot", Description: "Discord bot runtime", Subcommands: []Command{{Name: "run", Description: "Run the Discord bot"}}},
		{Name: "server", Description: "Minecraft server operations", Subcommands: []Command{{Name: "start"}, {Name: "stop"}, {Name: "status"}}},
		{Name: "config", Description: "Minecraft server configuration", Subcommands: []Command{{Name: "show"}, {Name: "get"}, {Name: "set"}, {Name: "init"}, {Name: "validate"}}},
		{Name: "env", Description: "Environment file management", Subcommands: []Command{{Name: "show"}, {Name: "get"}, {Name: "set"}, {Name: "unset"}, {Name: "init"}, {Name: "validate"}}},
		{Name: "setup", Description: "Guided setup flow", Subcommands: []Command{{Name: "env"}, {Name: "config"}}},
	}
}

func parseGlobalFlags(args []string, stdout, stderr io.Writer) (Options, []string, int) {
	var opts Options
	fs := flag.NewFlagSet("mcbot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON output for commands that support it")
	fs.BoolVar(&opts.Quiet, "quiet", false, "suppress non-essential output")
	fs.BoolVar(&opts.NoInput, "no-input", false, "fail instead of prompting")
	fs.BoolVar(&opts.Yes, "yes", false, "auto-confirm prompts")
	fs.BoolVar(&opts.Force, "force", false, "force supported init operations")
	fs.BoolVar(&opts.ShowSecrets, "show-secrets", false, "show secret values for env commands")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			printRootHelp(stdout)
			return opts, nil, ExitOK
		}
		fmt.Fprintf(stderr, "mcbot: %v\n", err)
		return opts, nil, ExitUsage
	}

	return opts, fs.Args(), -1
}

func validateGlobalFlagContract(opts Options, args []string, stderr io.Writer) int {
	if opts.ShowSecrets && firstArg(args) != "env" {
		return usageError(stderr, "--show-secrets is only supported for env commands")
	}
	if opts.ShowSecrets && len(args) >= 2 && (args[0] != "env" || (args[1] != "show" && args[1] != "get")) {
		return usageError(stderr, "--show-secrets is only supported for env show and env get")
	}
	if opts.JSON && !SupportsJSON(args) {
		return usageError(stderr, "--json is not supported for %s", commandPath(args))
	}
	if opts.Force && !SupportsForce(args) {
		return usageError(stderr, "--force is only supported for config init, env init, and setup")
	}
	return -1
}

func SupportsJSON(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "server":
		return args[1] == "status"
	case "config", "env":
		return args[1] == "show" || args[1] == "get" || args[1] == "validate"
	default:
		return false
	}
}

func SupportsForce(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "setup" {
		return len(args) <= 2
	}
	return len(args) >= 2 && (args[0] == "config" || args[0] == "env") && args[1] == "init"
}

func NeverPrompts(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if len(args) == 1 {
		return args[0] == "help" || args[0] == "version"
	}
	switch args[1] {
	case "validate", "show", "get", "status":
		return true
	default:
		return false
	}
}

func ConfirmPrompt(opts Options, promptRequired bool) (bool, error) {
	if !promptRequired {
		return true, nil
	}
	if opts.Yes {
		return true, nil
	}
	if opts.NoInput {
		return false, UsageError("prompt required but --no-input was set")
	}
	if opts.Force {
		return true, nil
	}
	return false, nil
}

func dispatchPlaceholder(opts Options, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if args[0] == "setup" {
		return dispatchSetup(opts, args[1:], stdin, stdout, stderr)
	}
	if len(args) < 2 {
		return usageError(stderr, "%s requires a subcommand", args[0])
	}
	if !isKnownSubcommand(args[0], args[1]) {
		return usageError(stderr, "unknown command %q", commandPath(args))
	}
	if args[0] == "bot" && args[1] == "run" {
		if len(args) > 2 {
			return usageError(stderr, "unknown command %q", commandPath(args))
		}
		if err := runBot(); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	}
	if args[0] == "server" {
		if len(args) > 2 {
			return usageError(stderr, "unknown command %q", commandPath(args))
		}
		return dispatchServer(opts, args[1], stdout, stderr)
	}
	if args[0] == "config" {
		return dispatchConfig(opts, args[1], args[2:], stdout, stderr)
	}
	return dispatchEnv(opts, args[1], args[2:], stdout, stderr)
}

func dispatchSetup(opts Options, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	subcommand, err := parseSetupArgs(&opts, args)
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	if opts.JSON {
		return usageError(stderr, "--json is not supported for %s", setupCommandPath(subcommand))
	}
	if opts.ShowSecrets {
		return usageError(stderr, "--show-secrets is only supported for env show and env get")
	}
	if opts.NoInput {
		_, err := ConfirmPrompt(opts, true)
		if err != nil {
			return usageError(stderr, "%v", err)
		}
	}
	if !opts.Yes && !opts.NoInput && !stdinAllowsPrompt(stdin) {
		return usageError(stderr, "prompt required but interactive input is unavailable")
	}

	prompter := NewStdlibPrompter(stdin, stdout)
	out := NewOutput(stdout, stderr, opts)
	if subcommand == "env" {
		if !opts.Yes {
			if err := out.SetupHeader("MCBot Setup", "Discord + Minecraft server wizard"); err != nil {
				fmt.Fprintf(stderr, "%v\n", InternalError("%v", err))
				return ExitInternal
			}
		}
		if err := confirmSetupOverwrite(opts, prompter, "setup env", defaultEnvPath()); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitCodeForError(err)
		}
		if err := runSetupEnv(opts, prompter, out, defaultEnvPath()); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitCodeForError(err)
		}
		return ExitOK
	}

	if subcommand == "config" {
		if !opts.Yes {
			if err := out.SetupHeader("MCBot Setup", "Discord + Minecraft server wizard"); err != nil {
				fmt.Fprintf(stderr, "%v\n", InternalError("%v", err))
				return ExitInternal
			}
		}
		if err := confirmSetupOverwrite(opts, prompter, "setup config", defaultConfigPath()); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitCodeForError(err)
		}
		cfg, err := collectSetupConfig(opts, prompter, out, defaultConfigPath(), setupConfigDisplay{
			header:            false,
			sectionOffset:     0,
			totalSections:     3,
			ownershipInConfig: true,
		})
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitCodeForError(err)
		}
		if !opts.Yes {
			if err := out.SetupSection(3, 3, "Review & write"); err != nil {
				fmt.Fprintf(stderr, "%v\n", InternalError("%v", err))
				return ExitInternal
			}
			if err := out.SetupReview("", nil, defaultConfigPath(), cfg); err != nil {
				fmt.Fprintf(stderr, "%v\n", InternalError("%v", err))
				return ExitInternal
			}
			confirmed, err := prompter.Confirm("Write mc-server.toml", true)
			if err != nil {
				fmt.Fprintf(stderr, "%v\n", OperationalError("read setup config write confirmation: %v", err))
				return ExitOperational
			}
			if !confirmed {
				fmt.Fprintf(stderr, "%v\n", UsageError("setup config cancelled before write"))
				return ExitUsage
			}
		}
		if err := writeSetupConfig(out, defaultConfigPath(), cfg); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitCodeForError(err)
		}
		if err := out.SetupComplete("", defaultConfigPath()); err != nil {
			fmt.Fprintf(stderr, "%v\n", InternalError("%v", err))
			return ExitInternal
		}
		return ExitOK
	}

	if err := runSetupCombined(opts, prompter, out); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitCodeForError(err)
	}
	return ExitOK
}

func stdinAllowsPrompt(stdin io.Reader) bool {
	if sized, ok := stdin.(interface{ Len() int }); ok && sized.Len() == 0 {
		return false
	}
	file, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	if file.Name() == os.DevNull {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	if nullInfo, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, nullInfo) {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func runSetupCombined(opts Options, prompter Prompter, out Output) error {
	envPath := defaultEnvPath()
	configPath := defaultConfigPath()
	if !opts.Yes {
		if err := out.SetupIntro(envPath, configPath); err != nil {
			return InternalError("%v", err)
		}
	}
	if err := confirmSetupOverwrite(opts, prompter, "setup env", envPath); err != nil {
		return err
	}
	if err := confirmSetupOverwrite(opts, prompter, "setup config", configPath); err != nil {
		return err
	}
	envResult, err := collectSetupEnv(opts, prompter, out, envPath, setupEnvDisplay{sectionIndex: 1, totalSections: 5})
	if err != nil {
		return err
	}
	defer envResult.cleanup()
	cfg, err := collectSetupConfig(opts, prompter, out, configPath, setupConfigDisplay{
		header:            false,
		sectionOffset:     2,
		totalSections:     5,
		ownershipSection:  false,
		ownershipInConfig: true,
	})
	if err != nil {
		return err
	}
	if err := out.SetupSection(5, 5, "Review & write"); err != nil {
		return InternalError("%v", err)
	}
	if err := out.SetupReview(envPath, envResult.entries, configPath, cfg); err != nil {
		return InternalError("%v", err)
	}
	if !opts.Yes {
		confirmed, err := prompter.Confirm("Write these files", true)
		if err != nil {
			return OperationalError("read setup write confirmation: %v", err)
		}
		if !confirmed {
			return UsageError("setup cancelled before write")
		}
	}
	if err := commitSetupEnvStage(envResult.stagedPath, envPath); err != nil {
		return ValidationError("%v", err)
	}
	if err := writeSetupConfig(out, configPath, cfg); err != nil {
		return err
	}
	if _, err := envfile.Validate(envPath); err != nil {
		return ValidationError("%v", err)
	}
	if err := mcconfig.ValidateFile(configPath); err != nil {
		return ValidationError("%v", err)
	}
	if err := out.SetupComplete(envPath, configPath); err != nil {
		return InternalError("%v", err)
	}
	return nil
}

func confirmSetupOverwrite(opts Options, prompter Prompter, command, path string) error {
	if opts.Yes || opts.Force {
		return nil
	}
	exists, err := fileExists(path)
	if err != nil {
		return ValidationError("%v", err)
	}
	if !exists {
		return nil
	}
	if opts.NoInput {
		return UsageError("%s would overwrite %s", command, path)
	}
	confirmed, err := prompter.Confirm(fmt.Sprintf("%s would overwrite %s", command, path), false)
	if err != nil {
		return OperationalError("read %s overwrite confirmation: %v", command, err)
	}
	if !confirmed {
		return UsageError("%s would overwrite %s", command, path)
	}
	return nil
}

func fileExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return true, nil
}

type setupOption struct {
	value       string
	label       string
	description string
	recommended bool
}

type setupConfigQuestion struct {
	key      string
	section  string
	title    string
	help     string
	options  []setupOption
	advanced bool
}

var setupConfigQuestions = []setupConfigQuestion{
	{key: "server.version", section: "Minecraft server", title: "Minecraft version"},
	{key: "server.type", section: "Minecraft server", title: "Server type", options: setupOptions("VANILLA", "FORGE", "FABRIC", "PAPER")},
	{key: "server.difficulty", section: "Minecraft server", title: "Difficulty", options: setupOptions("peaceful", "easy", "normal", "hard")},
	{key: "server.memory", section: "Minecraft server", title: "Max memory"},
	{key: "server.motd", section: "Minecraft server", title: "Server MOTD"},
	{key: "server.init_memory", section: "Minecraft server", title: "Initial memory", help: "Advanced. Keep this aligned with max memory unless you know why to tune it.", advanced: true},
	{key: "server.view_distance", section: "Minecraft server", title: "View distance", help: "Advanced. Higher values cost more CPU and memory.", advanced: true},
	{key: "server.simulation_distance", section: "Minecraft server", title: "Simulation distance", help: "Advanced. Higher values simulate more chunks around players.", advanced: true},
	{key: "container.restart_policy", section: "Container behavior", title: "Restart policy", help: "Choose whether Docker or the Discord bot/CLI owns Minecraft server restarts.", options: []setupOption{
		{value: "no", label: "no", description: "Docker will not auto-restart; recommended when Discord bot/CLI controls start and stop.", recommended: true},
		{value: "on-failure", label: "on-failure", description: "Docker restarts after crash/non-zero exit; bot crash/log visibility can be less precise unless runtime reconciliation is added."},
		{value: "unless-stopped", label: "unless-stopped", description: "Mostly-always-on mode; Docker restarts failures but keeps explicit manual stops across daemon restart."},
		{value: "always", label: "always", description: "24/7 Docker-owned mode; Docker may bring the server back after daemon restart."},
	}},
	{key: "container.port_publish", section: "Container behavior", title: "Port publish"},
}

func runSetupConfig(opts Options, prompter Prompter, stdout, stderr io.Writer) int {
	path := defaultConfigPath()
	out := NewOutput(stdout, stderr, opts)
	cfg, err := collectSetupConfig(opts, prompter, out, path, setupConfigDisplay{
		header:            !opts.Yes,
		sectionOffset:     0,
		totalSections:     3,
		ownershipInConfig: true,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitCodeForError(err)
	}
	if !opts.Yes {
		if err := out.SetupSection(3, 3, "Review & write"); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		if err := out.SetupReview("", nil, path, cfg); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		confirmed, err := prompter.Confirm("Write mc-server.toml", true)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", OperationalError("read setup config write confirmation: %v", err))
			return ExitOperational
		}
		if !confirmed {
			fmt.Fprintf(stderr, "%v\n", UsageError("setup config cancelled before write"))
			return ExitUsage
		}
	}
	if err := writeSetupConfig(out, path, cfg); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitCodeForError(err)
	}
	if err := out.SetupComplete("", path); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitInternal
	}
	return ExitOK
}

type setupConfigDisplay struct {
	header            bool
	sectionOffset     int
	totalSections     int
	ownershipSection  bool
	ownershipInConfig bool
}

func runSetupConfigWithOutput(opts Options, prompter Prompter, out Output, path string, display setupConfigDisplay) error {
	cfg, err := collectSetupConfig(opts, prompter, out, path, display)
	if err != nil {
		return err
	}
	return writeSetupConfig(out, path, cfg)
}

func collectSetupConfig(opts Options, prompter Prompter, out Output, path string, display setupConfigDisplay) (mcconfig.Config, error) {
	existingConfigFile, err := fileExists(path)
	if err != nil {
		return mcconfig.Config{}, ValidationError("%v", err)
	}
	cfg, loadedExistingConfig, err := loadSetupConfigSeed(path, existingConfigFile)
	if err != nil {
		return mcconfig.Config{}, ValidationError("%v", err)
	}

	if display.totalSections == 0 {
		display.totalSections = setupConfigSectionTotal(display.ownershipSection)
	}
	if display.header {
		if err := out.SetupHeader("MCBot Setup", "Discord + Minecraft server wizard"); err != nil {
			return mcconfig.Config{}, InternalError("%v", err)
		}
	}

	sectionIndex := 0
	lastSection := ""
	advancedServer := opts.Yes
	askedAdvancedServer := opts.Yes
	for _, question := range setupConfigQuestions {
		if question.section != lastSection {
			sectionIndex++
			lastSection = question.section
			if err := out.SetupSection(display.sectionOffset+sectionIndex, display.totalSections, question.section); err != nil {
				return mcconfig.Config{}, InternalError("%v", err)
			}
		}
		if question.advanced && question.section == "Minecraft server" && !askedAdvancedServer {
			var err error
			advancedServer, err = promptAdvancedSettings(prompter, out, "Minecraft server", "Configure advanced Minecraft settings?", false)
			askedAdvancedServer = true
			if err != nil {
				return mcconfig.Config{}, err
			}
		}
		if question.advanced && !advancedServer {
			continue
		}
		current, err := cfg.Value(question.key)
		if err != nil {
			return mcconfig.Config{}, ValidationError("%v", err)
		}
		defaultValue := fmt.Sprint(current)
		answer := defaultValue
		if !opts.Yes {
			answer, err = promptSetupConfigAnswer(prompter, out, question, defaultValue)
			if err != nil {
				return mcconfig.Config{}, ValidationError("%v", err)
			}
		}
		if err := applySetupConfigAnswer(&cfg, question.key, answer, prompter, opts.Yes); err != nil {
			return mcconfig.Config{}, ValidationError("%v", err)
		}
	}

	advancedContainer := opts.Yes
	if !opts.Yes {
		var err error
		advancedContainer, err = promptAdvancedSettings(prompter, out, "Container behavior", "Configure advanced container ownership?", false)
		if err != nil {
			return mcconfig.Config{}, err
		}
	}
	if display.ownershipSection && advancedContainer {
		if err := out.SetupSection(display.sectionOffset+sectionIndex+1, display.totalSections, "Container ownership"); err != nil {
			return mcconfig.Config{}, InternalError("%v", err)
		}
	} else if display.ownershipInConfig && advancedContainer {
		if err := out.SetupHint("Container ownership"); err != nil {
			return mcconfig.Config{}, InternalError("%v", err)
		}
	}
	if advancedContainer {
		if err := promptContainerOwnership(&cfg, prompter, opts, out, path, loadedExistingConfig); err != nil {
			return mcconfig.Config{}, ValidationError("%v", err)
		}
	} else if err := applyDefaultSetupOwnership(&cfg, path, loadedExistingConfig); err != nil {
		return mcconfig.Config{}, ValidationError("%v", err)
	}

	if err := cfg.Validate(); err != nil {
		return mcconfig.Config{}, ValidationError("%v", err)
	}
	return cfg, nil
}

func writeSetupConfig(_ Output, path string, cfg mcconfig.Config) error {
	if err := mcconfig.Write(path, cfg); err != nil {
		return ValidationError("%v", err)
	}
	return nil
}

func loadSetupConfigSeed(path string, existingConfigFile bool) (mcconfig.Config, bool, error) {
	cfg, err := mcconfig.Load(path)
	if err == nil {
		return cfg, existingConfigFile, nil
	}
	var invalidFileErr *mcconfig.InvalidFileError
	if errors.As(err, &invalidFileErr) {
		return mcconfig.Defaults(), false, nil
	}
	return mcconfig.Config{}, false, err
}

func promptSetupConfigAnswer(prompter Prompter, out Output, question setupConfigQuestion, defaultValue string) (string, error) {
	if err := out.SetupQuestion(question.section, question.title, question.help); err != nil {
		return "", err
	}
	if len(question.options) > 0 {
		options := make([]string, 0, len(question.options))
		for _, option := range question.options {
			display := option.label
			if option.description != "" {
				display += " — " + option.description
			}
			if option.recommended {
				display += " (recommended)"
			}
			options = append(options, display)
		}
		selected, err := prompter.Select("Choose", options, defaultSetupOptionIndex(question.options, defaultValue))
		if err != nil {
			return "", err
		}
		selectedIndex := indexOf(options, selected)
		if selectedIndex < 0 {
			selectedIndex = defaultSetupOptionIndex(question.options, defaultValue)
		}
		return question.options[selectedIndex].value, nil
	}
	return prompter.Input("Value", defaultValue)
}

func applySetupConfigAnswer(cfg *mcconfig.Config, key, answer string, prompter Prompter, nonInteractive bool) error {
	for {
		candidate := *cfg
		if err := mcconfig.ApplyValue(&candidate, key, answer); err != nil {
			if nonInteractive {
				return err
			}
			next, promptErr := promptSetupConfigRetry(prompter, key, cfg)
			if promptErr != nil {
				return promptErr
			}
			answer = next
			continue
		}
		if err := candidate.Validate(); err == nil {
			*cfg = candidate
			return nil
		} else if nonInteractive {
			return err
		} else if !strings.HasPrefix(err.Error(), key+" ") {
			return err
		}

		next, promptErr := promptSetupConfigRetry(prompter, key, cfg)
		if promptErr != nil {
			return promptErr
		}
		answer = next
	}
}

func promptSetupConfigRetry(prompter Prompter, key string, cfg *mcconfig.Config) (string, error) {
	current, err := cfg.Value(key)
	if err != nil {
		return "", err
	}
	return prompter.Input(setupQuestionLabel(setupQuestionByKey(key)), fmt.Sprint(current))
}

func setupQuestionByKey(key string) setupConfigQuestion {
	for _, question := range setupConfigQuestions {
		if question.key == key {
			return question
		}
	}
	return setupConfigQuestion{key: key, title: key}
}

func setupQuestionLabel(question setupConfigQuestion) string {
	title := question.title
	if title == "" {
		title = question.key
	}
	if title == question.key {
		return title
	}
	return fmt.Sprintf("%s (%s)", title, question.key)
}

func setupOptions(values ...string) []setupOption {
	options := make([]setupOption, 0, len(values))
	for _, value := range values {
		options = append(options, setupOption{value: value, label: value})
	}
	return options
}

func setupConfigSectionTotal(includeOwnership bool) int {
	total := 0
	lastSection := ""
	for _, question := range setupConfigQuestions {
		if question.section != lastSection {
			total++
			lastSection = question.section
		}
	}
	if includeOwnership {
		total++ // Container ownership is a dedicated final section for setup config.
	}
	return total
}

func defaultSetupOptionIndex(options []setupOption, current string) int {
	for i, option := range options {
		if option.value == current {
			return i
		}
	}
	for i, option := range options {
		if option.recommended {
			return i
		}
	}
	return 0
}

func parseSetupArgs(opts *Options, args []string) (string, error) {
	subcommand := ""
	for _, arg := range args {
		switch arg {
		case "--json":
			opts.JSON = true
		case "--show-secrets":
			opts.ShowSecrets = true
		case "env", "config":
			if subcommand != "" {
				return "", fmt.Errorf("setup accepts at most one subcommand")
			}
			subcommand = arg
		default:
			if strings.HasPrefix(arg, "--") {
				return "", fmt.Errorf("unknown setup flag %s", arg)
			}
			return "", fmt.Errorf("unknown command %q", "setup "+arg)
		}
	}
	return subcommand, nil
}

func setupCommandPath(subcommand string) string {
	if subcommand == "" {
		return "setup"
	}
	return "setup " + subcommand
}

func dispatchConfig(opts Options, subcommand string, args []string, stdout, stderr io.Writer) int {
	localOpts, path, positionals, err := parseConfigFlags(opts, args)
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	if localOpts.JSON && !SupportsJSON([]string{"config", subcommand}) {
		return usageError(stderr, "--json is not supported for config %s", subcommand)
	}
	out := NewOutput(stdout, stderr, localOpts)

	switch subcommand {
	case "init":
		if len(positionals) != 0 {
			return usageError(stderr, "config init does not accept arguments")
		}
		if exitCode := confirmExistingInit(localOpts, path, stderr, "config init"); exitCode >= 0 {
			return exitCode
		}
		if err := mcconfig.Init(path, localOpts.Force || localOpts.Yes); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if err := out.SuccessLine("initialized %s", path); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "show":
		if len(positionals) != 0 {
			return usageError(stderr, "config show does not accept arguments")
		}
		if err := requireExistingFile(path, "mc-server.toml"); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		cfg, err := mcconfig.Show(path)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if localOpts.JSON {
			if err := out.SuccessJSON(cfg); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessText("%s", mcconfig.Render(cfg)); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "get":
		if len(positionals) != 1 {
			return usageError(stderr, "config get requires exactly one key path")
		}
		if err := requireExistingFile(path, "mc-server.toml"); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		value, err := mcconfig.Get(path, positionals[0])
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if localOpts.JSON {
			payload := map[string]any{"key": positionals[0], "value": value}
			if err := out.SuccessJSON(payload); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessLine("%v", value); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "set":
		if len(positionals) != 2 {
			return usageError(stderr, "config set requires key path and value")
		}
		if err := mcconfig.Set(path, positionals[0], positionals[1]); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if err := out.SuccessLine("set %s", positionals[0]); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "validate":
		if len(positionals) != 0 {
			return usageError(stderr, "config validate does not accept arguments")
		}
		if err := mcconfig.ValidateFile(path); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if localOpts.JSON {
			if err := out.SuccessJSON(map[string]any{"path": path, "valid": true}); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessLine("%s is valid", path); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	default:
		return usageError(stderr, "unknown command %q", "config "+subcommand)
	}
}

func dispatchEnv(opts Options, subcommand string, args []string, stdout, stderr io.Writer) int {
	localOpts, path, positionals, err := parseEnvFlags(opts, args)
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	if localOpts.JSON && !SupportsJSON([]string{"env", subcommand}) {
		return usageError(stderr, "--json is not supported for env %s", subcommand)
	}
	if localOpts.ShowSecrets && subcommand != "show" && subcommand != "get" {
		return usageError(stderr, "--show-secrets is only supported for env show and env get")
	}
	out := NewOutput(stdout, stderr, localOpts)
	policy := envfile.RevealPolicy{ShowSecrets: localOpts.ShowSecrets}

	switch subcommand {
	case "init":
		if len(positionals) != 0 {
			return usageError(stderr, "env init does not accept arguments")
		}
		if exitCode := confirmExistingInit(localOpts, path, stderr, "env init"); exitCode >= 0 {
			return exitCode
		}
		if err := envfile.Init(path, localOpts.Force || localOpts.Yes); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if err := out.SuccessLine("initialized %s", path); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "show":
		if len(positionals) != 0 {
			return usageError(stderr, "env show does not accept arguments")
		}
		if err := requireExistingFile(path, ".env"); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		entries, err := envfile.Show(path, policy)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if localOpts.JSON {
			if err := out.SuccessJSON(entries); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessText("%s", envfile.Render(entries)); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "get":
		if len(positionals) != 1 {
			return usageError(stderr, "env get requires exactly one key")
		}
		if err := requireExistingFile(path, ".env"); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		entry, err := envfile.Get(path, positionals[0], policy)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if localOpts.JSON {
			if err := out.SuccessJSON(entry); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessLine("%s", entry.Value); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "set":
		if len(positionals) != 2 {
			return usageError(stderr, "env set requires key and value")
		}
		if err := envfile.Set(path, positionals[0], positionals[1]); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if err := out.SuccessLine("set %s", positionals[0]); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "unset":
		if len(positionals) != 1 {
			return usageError(stderr, "env unset requires exactly one key")
		}
		if err := envfile.Unset(path, positionals[0]); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if err := out.SuccessLine("unset %s", positionals[0]); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "validate":
		if len(positionals) != 0 {
			return usageError(stderr, "env validate does not accept arguments")
		}
		result, err := envfile.Validate(path)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if localOpts.JSON {
			if err := out.SuccessJSON(result); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessLine("%s is valid", path); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	default:
		return usageError(stderr, "unknown command %q", "env "+subcommand)
	}
}

func parseEnvFlags(opts Options, args []string) (Options, string, []string, error) {
	path := defaultEnvPath()
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--show-secrets":
			opts.ShowSecrets = true
		case "--force":
			opts.Force = true
		case "--yes":
			opts.Yes = true
		case "--file":
			i++
			if i >= len(args) || args[i] == "" {
				return opts, path, nil, fmt.Errorf("--file requires a path")
			}
			path = args[i]
		default:
			if strings.HasPrefix(args[i], "--") {
				return opts, path, nil, fmt.Errorf("unknown env flag %s", args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	return opts, path, positionals, nil
}

func parseConfigFlags(opts Options, args []string) (Options, string, []string, error) {
	path := defaultConfigPath()
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--force":
			opts.Force = true
		case "--yes":
			opts.Yes = true
		case "--file":
			i++
			if i >= len(args) || args[i] == "" {
				return opts, path, nil, fmt.Errorf("--file requires a path")
			}
			path = args[i]
		default:
			if strings.HasPrefix(args[i], "--") {
				return opts, path, nil, fmt.Errorf("unknown config flag %s", args[i])
			}
			positionals = append(positionals, args[i])
		}
	}
	return opts, path, positionals, nil
}

func confirmExistingInit(opts Options, path string, stderr io.Writer, command string) int {
	if opts.Force || opts.Yes {
		return -1
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return -1
		}
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitValidation
	}
	if opts.NoInput {
		return usageError(stderr, "%s would overwrite %s; use --force or --yes to overwrite", command, path)
	}
	return usageError(stderr, "%s would overwrite %s; use --force or --yes to overwrite", command, path)
}

func requireExistingFile(path, label string) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s does not exist: %s", label, path)
		}
		return fmt.Errorf("stat %s: %w", label, err)
	}
	return nil
}

func defaultConfigPath() string {
	if configPathOverride != "" {
		return configPathOverride
	}
	return discoverDefaultPaths().ConfigFile
}

func defaultEnvPath() string {
	if envPathOverride != "" {
		return envPathOverride
	}
	return discoverDefaultPaths().EnvFile
}

func discoverDefaultPaths() composectl.Paths {
	paths, err := composectl.DiscoverRepoRoot("")
	if err == nil {
		return paths
	}
	return composectl.DefaultPaths(".")
}

func dispatchServer(opts Options, subcommand string, stdout, stderr io.Writer) int {
	out := NewOutput(stdout, stderr, opts)
	var result serverops.Result
	var err error
	switch subcommand {
	case "start":
		result, err = serverRunner.Start(context.Background())
	case "stop":
		result, err = serverRunner.Stop(context.Background())
	case "status":
		result, err = serverRunner.Status(context.Background())
	default:
		return usageError(stderr, "unknown command %q", "server "+subcommand)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitOperational
	}
	if opts.JSON {
		if err := out.SuccessJSON(result); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	}
	for _, warning := range result.Warnings {
		if err := out.Diagnostic("warning: %s", warning); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
	}
	message := result.Message
	if message == "" {
		message = fmt.Sprintf("%s: %s", result.Service, result.Status)
	}
	if err := out.SuccessLine("%s", message); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitInternal
	}
	return ExitOK
}

func isKnownSubcommand(commandName, subcommandName string) bool {
	for _, command := range CommandTree() {
		if command.Name != commandName {
			continue
		}
		for _, subcommand := range command.Subcommands {
			if subcommand.Name == subcommandName {
				return true
			}
		}
	}
	return false
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func isPromptCapablePath(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "config", "env":
		return args[1] == "init" || args[1] == "set" || args[1] == "unset"
	default:
		return false
	}
}

func commandPath(args []string) string {
	if len(args) >= 2 {
		return args[0] + " " + args[1]
	}
	return firstArg(args)
}

func printRootHelp(w io.Writer) {
	fmt.Fprintln(w, "MCBot operational CLI")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  mcbot [global options] <command> [args]")
	fmt.Fprintln(w, "  mcbot help")
	fmt.Fprintln(w, "  mcbot version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, command := range CommandTree() {
		if len(command.Subcommands) == 0 {
			fmt.Fprintf(w, "  %-8s %s\n", command.Name, command.Description)
			continue
		}
		fmt.Fprintf(w, "  %-8s %s\n", command.Name, command.Description)
		for _, subcommand := range command.Subcommands {
			fmt.Fprintf(w, "    %s %s\n", command.Name, subcommand.Name)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global options:")
	fmt.Fprintln(w, "  --json       emit JSON output for commands that support it")
	fmt.Fprintln(w, "  --quiet      suppress non-essential output")
	fmt.Fprintln(w, "  --no-input   fail instead of prompting")
	fmt.Fprintln(w, "  --yes        auto-confirm prompts")
	fmt.Fprintln(w, "  --force      force supported init operations")
	fmt.Fprintln(w, "  --show-secrets show secret values for env commands")
}

func usageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "mcbot: "+format+"\n", args...)
	fmt.Fprintln(stderr, "Run 'mcbot help' for usage.")
	return ExitUsage
}
