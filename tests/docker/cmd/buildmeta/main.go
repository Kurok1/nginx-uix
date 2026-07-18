/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kuroky/nginx-uix/tests/docker/internal/buildmeta"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "buildmeta: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("command must be source or identity")
	}
	switch arguments[0] {
	case "source":
		return runSource(ctx, arguments[1:], stdout)
	case "identity":
		return runIdentity(arguments[1:], stdout)
	default:
		return fmt.Errorf("unsupported command %q", arguments[0])
	}
}

func runSource(ctx context.Context, arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("source", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "source context root")
	ignore := flags.String("ignore", ".dockerignore", "literal ignore file")
	if err := flags.Parse(arguments); err != nil {
		return errors.New("invalid source arguments")
	}
	if flags.NArg() != 0 {
		return errors.New("source command has unexpected positional arguments")
	}
	digest, err := buildmeta.SourceFingerprint(ctx, buildmeta.SourceOptions{Root: *root, IgnoreFile: *ignore})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, digest.String())
	return err
}

func runIdentity(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("identity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	source := flags.String("source", "", "source fingerprint")
	platform := flags.String("platform", "", "target platform")
	bases := newKeyValueFlag("base")
	buildArgs := newKeyValueFlag("arg")
	flags.Var(bases, "base", "base image name=digest")
	flags.Var(buildArgs, "arg", "build argument name=value")
	if err := flags.Parse(arguments); err != nil {
		return errors.New("invalid identity arguments")
	}
	if flags.NArg() != 0 {
		return errors.New("identity command has unexpected positional arguments")
	}
	sourceDigest, err := buildmeta.ParseDigest(*source)
	if err != nil {
		return fmt.Errorf("source fingerprint: %w", err)
	}
	digest, err := buildmeta.BuildIdentity(buildmeta.IdentityInput{
		SourceFingerprint: sourceDigest,
		Platform:          *platform,
		BaseDigests:       bases.values,
		BuildArgs:         buildArgs.values,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, digest.String())
	return err
}

type keyValueFlag struct {
	name   string
	values map[string]string
}

func newKeyValueFlag(name string) *keyValueFlag {
	return &keyValueFlag{name: name, values: make(map[string]string)}
}

func (value *keyValueFlag) String() string {
	return ""
}

func (value *keyValueFlag) Set(raw string) error {
	key, item, found := strings.Cut(raw, "=")
	if !found || key == "" || item == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n\t ") {
		return fmt.Errorf("%s must use non-empty name=value", value.name)
	}
	if _, exists := value.values[key]; exists {
		return fmt.Errorf("duplicate %s %q", value.name, key)
	}
	value.values[key] = item
	return nil
}
