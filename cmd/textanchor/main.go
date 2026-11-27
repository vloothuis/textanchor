// Command textanchor views and edits review bundles: zip files that hold a
// Markdown document and comments anchored to its text.
//
// Run "textanchor help" for usage. The bundle format is described in
// docs/bundle-format.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = ""

const usage = `textanchor views and edits review bundles (.mdreview files).

Usage:
  textanchor init <doc.md> [-o out.mdreview] [--force]
  textanchor show <bundle> [--resolved]
  textanchor list <bundle> [--resolved] [--json]
  textanchor add <bundle> (--quote TEXT [--nth N] | --line N[-M]) [-m BODY]
  textanchor reply <bundle> <id> [-m BODY]
  textanchor edit <bundle> <id> [-m BODY]
  textanchor resolve <bundle> <id>
  textanchor reopen <bundle> <id>
  textanchor delete <bundle> <id>
  textanchor merge <bundle> <other>...
  textanchor extract <bundle> [-o doc.md] [--force]
  textanchor update <bundle> <doc.md>
  textanchor version

An <id> may be shortened to a unique prefix of at least 4 characters.
Without -m, the comment text is read from standard input.

Common flags:
  --author NAME    author of new comments; default $TEXTANCHOR_AUTHOR,
                   then git config user.name, then your login name
  --color WHEN     auto, always or never (default auto; honours NO_COLOR)

Run "textanchor <command> -h" for the flags of one command.
`

// errUsage marks errors caused by wrong arguments; they exit with status 2.
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

func usageErr(format string, args ...any) error {
	return errUsage{fmt.Sprintf(format, args...)}
}

type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// isTerminal reports whether stdout is a terminal.
	isTerminal  func() bool
	stdinIsTerm func() bool
	getenv      func(string) string
}

func main() {
	e := &env{
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		isTerminal:  func() bool { return isTerminal(os.Stdout) },
		stdinIsTerm: func() bool { return isTerminal(os.Stdin) },
		getenv:      os.Getenv,
	}
	os.Exit(run(e, os.Args[1:]))
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func run(e *env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(e.stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(e.stdout, "textanchor", buildVersion())
		return 0
	case "init":
		err = cmdInit(e, rest)
	case "show":
		err = cmdShow(e, rest)
	case "list":
		err = cmdList(e, rest)
	case "add":
		err = cmdAdd(e, rest)
	case "reply":
		err = cmdReply(e, rest)
	case "edit":
		err = cmdEdit(e, rest)
	case "resolve":
		err = cmdResolve(e, rest, true)
	case "reopen":
		err = cmdResolve(e, rest, false)
	case "delete":
		err = cmdDelete(e, rest)
	case "merge":
		err = cmdMerge(e, rest)
	case "extract":
		err = cmdExtract(e, rest)
	case "update":
		err = cmdUpdate(e, rest)
	default:
		err = usageErr("unknown command %q", cmd)
	}
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	// Errors can quote bundle content, so they are sanitized like any output.
	fmt.Fprintln(e.stderr, "textanchor:", sanitize(err.Error()))
	var u errUsage
	if errors.As(err, &u) {
		fmt.Fprintln(e.stderr, `Run "textanchor help" for usage.`)
		return 2
	}
	return 1
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

// parse parses flags and positional arguments in any order, so that
// "textanchor add file.mdreview --quote x" works. It requires exactly want
// positional arguments, or at least -want if want is negative.
func parse(fs *flag.FlagSet, args []string, want int, names string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageErr("%s %s: %v", fs.Name(), names, err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		if args[0] == "--" {
			pos = append(pos, args[1:]...)
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	if (want >= 0 && len(pos) != want) || (want < 0 && len(pos) < -want) {
		return nil, usageErr("usage: textanchor %s %s", fs.Name(), names)
	}
	return pos, nil
}

// helpFlags prints a command's flags when -h is given.
func helpFlags(e *env, fs *flag.FlagSet, names string) {
	fs.Usage = func() {
		fmt.Fprintf(e.stdout, "Usage: textanchor %s %s\n", fs.Name(), names)
		var b strings.Builder
		fs.SetOutput(&b)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
		fmt.Fprint(e.stdout, b.String())
	}
}
