package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/settings"
	"github.com/ismaelosuna7824/nimbo/backend/internal/setup"
)

// doctorCmd prints the setup checks and exits 1 when a required one fails.
// It only reads the home directory (settings.json), never creates it.
func doctorCmd(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	homeFlag := fs.String("home", "", "data directory (default $NIMBO_HOME or ~/.nimbo)")
	fs.Parse(args)

	home := *homeFlag
	if home == "" {
		home = os.Getenv("NIMBO_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(userHome, ".nimbo")
	}
	prefs, err := settings.Open(home)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st := setup.New(setup.Config{Home: home, Settings: prefs.Get}).Run(ctx)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			return err
		}
	} else {
		printDoctor(os.Stdout, st, useColor(os.Stdout))
	}
	if !st.Ready {
		os.Exit(1)
	}
	return nil
}

// useColor reports whether f is a terminal and NO_COLOR is unset.
func useColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

func printDoctor(w io.Writer, st model.SetupStatus, color bool) {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + ansiReset
	}
	header := "Nimbo doctor · " + st.OSName + " (" + st.Arch + ")"
	if st.PackageManager != "" {
		header += " · package manager: " + st.PackageManager
	}
	fmt.Fprintln(w, paint(ansiBold, header))
	fmt.Fprintln(w)

	width := 0
	for _, ch := range st.Checks {
		width = max(width, len([]rune(ch.Title)))
	}
	for _, ch := range st.Checks {
		var mark string
		pending := ch.Status == model.SetupMissing && len(ch.Fixes) == 0 // waits on an earlier step
		switch {
		case ch.Status == model.SetupOK:
			mark = paint(ansiGreen, "✓")
		case ch.Status == model.SetupWarning:
			mark = paint(ansiYellow, "!")
		case pending || !ch.Required:
			mark = paint(ansiDim, "-")
		default:
			mark = paint(ansiRed, "✗")
		}
		pad := strings.Repeat(" ", width-len([]rune(ch.Title)))
		fmt.Fprintf(w, "  %s %s%s  %s\n", mark, ch.Title, pad, paint(ansiDim, ch.Summary))
		if ch.Status == model.SetupOK || pending {
			continue
		}
		for _, line := range wrap(ch.Detail, 74) {
			fmt.Fprintln(w, "      "+line)
		}
		for _, f := range ch.Fixes {
			switch {
			case f.Kind == model.FixLink:
				fmt.Fprintf(w, "      → %s: %s\n", f.Label, paint(ansiCyan, f.URL))
			case f.Command != "":
				suffix := ""
				if f.Kind == model.FixAction {
					suffix = " (one click in the Nimbo app, or run)"
				}
				fmt.Fprintf(w, "      → %s%s:\n          %s\n", f.Label, suffix, paint(ansiCyan, f.Command))
			default:
				fmt.Fprintf(w, "      → %s (one click in the Nimbo app's setup screen)\n", f.Label)
			}
		}
	}
	fmt.Fprintln(w)
	if st.Ready {
		fmt.Fprintln(w, paint(ansiGreen+ansiBold, "Ready: Nimbo can run sandboxes on this machine."))
	} else {
		fmt.Fprintln(w, paint(ansiRed+ansiBold, "Not ready yet: fix the items marked ✗ above, then run `nimbo doctor` again."))
	}
}

// wrap splits s into lines of at most width runes at spaces.
func wrap(s string, width int) []string {
	var lines []string
	var cur strings.Builder
	n := 0
	for word := range strings.FieldsSeq(s) {
		wl := len([]rune(word))
		if n > 0 && n+1+wl > width {
			lines = append(lines, cur.String())
			cur.Reset()
			n = 0
		}
		if n > 0 {
			cur.WriteByte(' ')
			n++
		}
		cur.WriteString(word)
		n += wl
	}
	if n > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
