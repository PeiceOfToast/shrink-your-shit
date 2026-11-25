// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

// resetPasswordCommand is "shrink-your-shit reset-password [-data <folder>]": it sets the sign-in password
// of the server on this computer, for someone who has forgotten it. In a terminal the password is typed
// twice with echo off; from a pipe it is read once. It never prints the password.
func resetPasswordCommand(args []string, in *os.File, out, errOut io.Writer) int {
	fl := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fl.SetOutput(errOut)
	data := fl.String("data", "", "the service's data folder (default: the installed service's)")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		fmt.Fprintln(errOut, "usage: shrink-your-shit reset-password [-data <folder>]")
		return 2
	}
	dir := *data
	if dir == "" {
		dir = signinDir(envFile(linuxEnvFile))
	}
	if dir == "" {
		fmt.Fprintln(errOut, "No data folder found. Give the one the server uses with -data <folder>.")
		return 1
	}
	pw, err := readNewPassword(in, errOut)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := web.ResetPassword(dir, pw); err != nil {
		fmt.Fprintf(errOut, "The password was not changed: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "The sign-in password is changed in %s. Browsers that were signed in with the old one are signed out. A running server uses the new one without a restart.\n", dir)
	return 0
}

// signinDir is the first data folder that holds a server's sign-in files ("" when none does).
func signinDir(env map[string]string) string {
	for _, dir := range tokenDirs(env) {
		for _, name := range []string{web.SigninFile, web.TokenFile} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return dir
			}
		}
	}
	return ""
}

// readNewPassword reads the new password: typed twice with echo off in a terminal, else one line from in.
func readNewPassword(in *os.File, errOut io.Writer) (string, error) {
	r := bufio.NewReader(in)
	if st, err := in.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("no password was given")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	first, err := readHidden(in, r, errOut, "New password: ")
	if err != nil {
		return "", err
	}
	again, err := readHidden(in, r, errOut, "Type it again: ")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", fmt.Errorf("The two passwords do not match. Nothing was changed.")
	}
	return first, nil
}

// readHidden reads one line from the terminal in with echo off (stty), so the typing does not show.
func readHidden(in *os.File, r *bufio.Reader, errOut io.Writer, prompt string) (string, error) {
	off := exec.Command("stty", "-echo")
	off.Stdin = in
	if err := off.Run(); err != nil {
		return "", fmt.Errorf("Cannot hide what you type here. Run it in a terminal that can hide input, or pipe the new password in on its own line.")
	}
	defer func() {
		on := exec.Command("stty", "echo")
		on.Stdin = in
		on.Run()
		fmt.Fprintln(errOut)
	}()
	fmt.Fprint(errOut, prompt)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no password was given")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
