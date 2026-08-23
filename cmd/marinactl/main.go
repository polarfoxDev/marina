// Command marinactl reads Marina backups directly from S3: it can list the
// complete snapshots for an instance and restore one to a local directory.
// It is the only tool (other than the standalone `age` CLI, for encrypted
// archives, plus zstd and tar) that can read a Marina backup back -- there
// is no restic-equivalent external tool to fall back on.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/polarfoxDev/marina/internal/backend"
	"github.com/polarfoxDev/marina/internal/config"
	"github.com/polarfoxDev/marina/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "list":
		runList(os.Args[2:])
	case "restore":
		runRestore(os.Args[2:])
	case "-version", "--version":
		fmt.Printf("marinactl version %s\n", version.Version)
	case "-h", "-help", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `marinactl - list and restore Marina S3 backups

Usage:
  marinactl list <instance-id> [flags]
  marinactl restore <instance-id> <timestamp|latest> --out <dir> [flags]

Common flags:
  --config <path>    config file (default: $CONFIG_FILE or /config.yml)
  --password <pw>    encryption password override (default: $MARINA_ENC_PASSWORD,
                      falling back to the instance's configured encryption.password)

Restore flags:
  --out <dir>        directory to restore into (required)
  --target <type:name>
                      restore only this target, e.g. --target volume:app-uploads;
                      may be repeated. Default: restore every target in the snapshot.
`)
}

// commonFlags holds the config/password overrides shared by both subcommands.
type commonFlags struct {
	configPath  string
	password    string
	passwordSet bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", envDefault("CONFIG_FILE", "/config.yml"), "config file path")
	fs.Func("password", "encryption password override", func(v string) error {
		c.password = v
		c.passwordSet = true
		return nil
	})
}

// reorderArgs moves every recognized flag (and its value) in args to the
// front, preserving the relative order of both flags and positional
// arguments otherwise. The standard flag package stops parsing at the
// first non-flag token, which would otherwise force
// "marinactl restore --out d id ts" and reject the more natural
// "marinactl restore id ts --out d". fs must have every flag it accepts
// already registered (via StringVar/Func/etc.) before this is called.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	known := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { known[f.Name] = true })

	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		rest, isFlag := strings.CutPrefix(a, "--")
		if !isFlag {
			rest, isFlag = strings.CutPrefix(a, "-")
		}
		if isFlag && rest != "" {
			name, hasValue := rest, false
			if idx := strings.IndexByte(rest, '='); idx >= 0 {
				name, hasValue = rest[:idx], true
			}
			if known[name] {
				flagArgs = append(flagArgs, a)
				if !hasValue && i+1 < len(args) {
					i++
					flagArgs = append(flagArgs, args[i])
				}
				continue
			}
		}
		positional = append(positional, a)
	}
	return append(flagArgs, positional...)
}

func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var common commonFlags
	common.register(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: marinactl list <instance-id>")
		os.Exit(2)
	}
	instanceID := fs.Arg(0)

	b := mustLoadS3Backend(instanceID, &common)
	ctx := context.Background()

	refs, err := b.ListSnapshots(ctx)
	if err != nil {
		fatalf("list snapshots: %v", err)
	}
	if len(refs) == 0 {
		fmt.Println("no complete snapshots found")
		return
	}

	fmt.Printf("%-20s  %-25s  %s\n", "TIMESTAMP", "TIME", "TARGETS")
	for _, ref := range refs {
		man, err := b.GetManifest(ctx, ref.Timestamp)
		targets := ""
		switch {
		case err == nil:
			names := make([]string, 0, len(man.Artifacts))
			for _, a := range man.Artifacts {
				names = append(names, fmt.Sprintf("%s:%s", a.Type, a.Name))
			}
			targets = strings.Join(names, ", ")
		case b.Password == "":
			// No password configured at all: most likely this manifest is
			// encrypted and we never even attempted decryption (Decrypt
			// passes empty-password reads straight through, so this shows
			// up as a JSON parse error, not an auth error).
			targets = "(need password to list)"
		default:
			targets = fmt.Sprintf("(error reading manifest: %v)", err)
		}
		fmt.Printf("%-20s  %-25s  %s\n", ref.Timestamp, ref.Time.Format(time.RFC3339), targets)
	}
}

func runRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	var common commonFlags
	common.register(fs)
	out := fs.String("out", "", "directory to restore into (required)")
	var targetFilters []string
	fs.Func("target", "restore only this target (type:name); may be repeated", func(v string) error {
		targetFilters = append(targetFilters, v)
		return nil
	})
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: marinactl restore <instance-id> <timestamp|latest> --out <dir>")
		os.Exit(2)
	}
	if *out == "" {
		fatalf("--out is required")
	}
	instanceID := fs.Arg(0)
	requestedTimestamp := fs.Arg(1)

	b := mustLoadS3Backend(instanceID, &common)
	ctx := context.Background()

	timestamp := requestedTimestamp
	if requestedTimestamp == "latest" {
		refs, err := b.ListSnapshots(ctx)
		if err != nil {
			fatalf("list snapshots: %v", err)
		}
		if len(refs) == 0 {
			fatalf("no complete snapshots found for instance %q", instanceID)
		}
		timestamp = refs[0].Timestamp // ListSnapshots returns newest first
	}

	man, err := b.GetManifest(ctx, timestamp)
	if err != nil {
		fatalf("read manifest for %s/%s: %v (wrong --password?)", instanceID, timestamp, err)
	}

	artifacts := man.Artifacts
	if len(targetFilters) > 0 {
		wanted := make(map[string]bool, len(targetFilters))
		for _, t := range targetFilters {
			wanted[t] = true
		}
		var filtered []backend.ManifestArtifact
		for _, a := range man.Artifacts {
			if wanted[fmt.Sprintf("%s:%s", a.Type, a.Name)] {
				filtered = append(filtered, a)
			}
		}
		artifacts = filtered
		if len(artifacts) == 0 {
			fatalf("none of the requested --target filters matched a target in snapshot %s (available: %s)", timestamp, describeArtifacts(man.Artifacts))
		}
	}

	outAbs, err := filepath.Abs(*out)
	if err != nil {
		fatalf("resolve --out path: %v", err)
	}

	fmt.Printf("restoring %d target(s) from %s/%s into %s\n", len(artifacts), instanceID, timestamp, outAbs)
	for _, art := range artifacts {
		dest := filepath.Join(outAbs, art.Type, art.Name)
		fmt.Printf("  %s:%s -> %s\n", art.Type, art.Name, dest)
		if err := b.RestoreArtifact(ctx, timestamp, art, dest); err != nil {
			fatalf("restore %s:%s: %v", art.Type, art.Name, err)
		}
	}
	fmt.Println("restore complete")
}

func describeArtifacts(artifacts []backend.ManifestArtifact) string {
	names := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		names = append(names, fmt.Sprintf("%s:%s", a.Type, a.Name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// mustLoadS3Backend loads the config, finds instanceID, and builds an
// S3Backend for its read path. Exits the process on any failure -- this is
// a CLI, not a library.
func mustLoadS3Backend(instanceID string, common *commonFlags) *backend.S3Backend {
	cfg, err := config.Load(common.configPath)
	if err != nil {
		fatalf("load config %s: %v", common.configPath, err)
	}

	dest, err := cfg.GetDestination(instanceID)
	if err != nil {
		fatalf("%v", err)
	}
	if dest.S3 == nil {
		fatalf("instance %q is not an S3 destination (marinactl only reads S3 backups; custom-image backends manage their own storage)", instanceID)
	}

	password := ""
	if dest.Encryption != nil {
		password = dest.Encryption.Password
	}
	if common.passwordSet {
		password = common.password
	} else if envPw := os.Getenv("MARINA_ENC_PASSWORD"); envPw != "" {
		password = envPw
	}

	b := &backend.S3Backend{
		ID:              dest.ID,
		Endpoint:        dest.S3.Endpoint,
		Region:          dest.S3.Region,
		Bucket:          dest.S3.Bucket,
		Prefix:          dest.S3.Prefix,
		AccessKeyID:     dest.S3.AccessKeyID,
		SecretAccessKey: dest.S3.SecretAccessKey,
		UseSSL:          dest.S3.UseSSL == nil || *dest.S3.UseSSL,
		ForcePathStyle:  dest.S3.ForcePathStyle,
		Password:        password,
		MarinaVersion:   version.Version,
	}
	if err := b.EnsureClient(); err != nil {
		fatalf("connect to S3: %v", err)
	}
	return b
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "marinactl: "+format+"\n", args...)
	os.Exit(1)
}
