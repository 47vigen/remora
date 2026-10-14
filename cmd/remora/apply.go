package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/47vigen/remora/internal/config"
)

const applyUsage = "usage: remora apply -f FILE [--yes] [--prune=true] [--url URL] [--token TOKEN]"

// runApply implements `remora apply -f FILE [--yes] [--prune=true]` against a running remora. The file is parsed
// locally first. Every env:NAME it references and env holds is sent as a secret (an unset one keeps the stored
// secret). The server's plan is printed; without --yes nothing changes, with it the plan is applied. With --prune
// (the default) what remora has and the file does not is deleted.
func runApply(ctx context.Context, args []string, stdout io.Writer, env func(string) (string, bool)) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), applyUsage)
		fs.PrintDefaults()
	}
	path := fs.String("f", "", "the remora.yaml to apply")
	yes := fs.Bool("yes", false, "make the changes; without it, only show them")
	prune := fs.Bool("prune", true, "delete what remora has and the file does not")
	var conn connFlags
	conn.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return errors.New(applyUsage)
	}

	doc, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	f, err := config.Parse(doc)
	if err != nil {
		return err
	}
	sec := referencedSecrets(f, env)

	c, err := conn.dial(ctx, env)
	if err != nil {
		return err
	}
	req := configRequest(doc, sec, *prune)
	changes, err := planConfig(ctx, c, req)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(stdout, "No changes.")
		return nil
	}
	printChanges(stdout, changes)
	if !*yes {
		_, _ = fmt.Fprintln(stdout, "Run again with --yes to apply.")
		return nil
	}
	applied, err := applyConfig(ctx, c, req)
	if err != nil {
		return err
	}
	noun := "changes"
	if len(applied) == 1 {
		noun = "change"
	}
	_, _ = fmt.Fprintf(stdout, "Applied %d %s.\n", len(applied), noun)
	printGenerated(stdout, applied)
	return nil
}

// referencedSecrets reads every env:NAME the document references from env; only those are sent.
func referencedSecrets(f config.File, env func(string) (string, bool)) config.Secrets {
	sec := config.Secrets{}
	add := func(ref string) {
		name, err := config.SecretRef(ref)
		if err != nil {
			return
		}
		if v, ok := env(name); ok {
			sec[name] = v
		}
	}
	for _, p := range f.Providers {
		for _, k := range p.Keys {
			add(k.Value)
		}
	}
	for _, k := range f.Keys {
		if k.Value != "" {
			add(k.Value)
		}
	}
	return sec
}
