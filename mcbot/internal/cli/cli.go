package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
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
	case "bot", "server", "config", "env":
		return dispatchPlaceholder(opts, remaining, stdout, stderr)
	default:
		return usageError(stderr, "unknown command %q", remaining[0])
	}
}

func NewOutput(stdout, stderr io.Writer, opts Options) Output {
	return Output{stdout: stdout, stderr: stderr, quiet: opts.Quiet}
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
		return usageError(stderr, "--force is only supported for config init and env init")
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
	if opts.Yes || opts.Force {
		return true, nil
	}
	if opts.NoInput {
		return false, UsageError("prompt required but --no-input was set")
	}
	return false, nil
}

func dispatchPlaceholder(opts Options, args []string, stdout, stderr io.Writer) int {
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
