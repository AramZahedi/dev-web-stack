//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func isElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	if token.IsElevated() {
		return true
	}

	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}

func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cwd, _ := os.Getwd()
	verb := windows.StringToUTF16Ptr("runas")
	exePtr := windows.StringToUTF16Ptr(exe)
	cwdPtr := windows.StringToUTF16Ptr(cwd)

	var escaped []string
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			escaped = append(escaped, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
		} else {
			escaped = append(escaped, a)
		}
	}
	params := strings.Join(escaped, " ")
	paramsPtr := windows.StringToUTF16Ptr(params)

	if err := windows.ShellExecute(0, verb, exePtr, paramsPtr, cwdPtr, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("request administrator elevation: %w", err)
	}

	os.Exit(0)
	return nil
}
