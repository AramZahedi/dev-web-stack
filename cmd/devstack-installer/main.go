package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "devstack-installer: %v\n", err)
		os.Exit(1)
	}

	if opts.Help {
		printUsage()
		os.Exit(0)
	}

	// 1. Ensure Administrator elevation
	if !opts.DryRun && !isElevated() {
		fmt.Println("devstack-installer: Administrator privileges required. Requesting elevation...")
		if err := relaunchElevated(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "devstack-installer: failed to elevate: %v\n", err)
			os.Exit(2)
		}
		return
	}

	printBanner(opts.Uninstall)

	// 2. Prepare staging directory and unpack payload
	stageDir, err := os.MkdirTemp("", "devstack-install-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "devstack-installer: failed to create temporary directory: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	scriptName := "install.ps1"
	if opts.Uninstall {
		scriptName = "uninstall.ps1"
	}

	scriptPath := ""
	// Check if running from repository with local script
	if exe, err := os.Executable(); err == nil {
		localScript := filepath.Join(filepath.Dir(exe), scriptName)
		if fi, err := os.Stat(localScript); err == nil && !fi.IsDir() {
			scriptPath = localScript
		}
	}
	if scriptPath == "" {
		if fi, err := os.Stat(scriptName); err == nil && !fi.IsDir() {
			scriptPath, _ = filepath.Abs(scriptName)
		}
	}
	// If uninstalling, also check if script exists in the target installation directory
	if scriptPath == "" && opts.Uninstall && opts.InstallDir != "" {
		targetScript := filepath.Join(opts.InstallDir, scriptName)
		if fi, err := os.Stat(targetScript); err == nil && !fi.IsDir() {
			scriptPath = targetScript
		}
	}

	if scriptPath == "" || len(embeddedPayload) > 22 || opts.ZipPath != "" {
		payloadReader, size, err := locatePayload(opts)
		if err != nil && scriptPath == "" {
			fmt.Fprintf(os.Stderr, "devstack-installer: %v\n", err)
			pauseIfInteractive(opts.Unattended)
			os.Exit(1)
		}
		if payloadReader != nil {
			if closer, ok := payloadReader.(interface{ Close() error }); ok {
				defer closer.Close()
			}
			fmt.Println("devstack-installer: Unpacking installer assets...")
			if err := extractZip(payloadReader, size, stageDir); err != nil {
				fmt.Fprintf(os.Stderr, "devstack-installer: extraction failed: %v\n", err)
				pauseIfInteractive(opts.Unattended)
				os.Exit(1)
			}
			scriptPath = filepath.Join(stageDir, scriptName)
		}
	}

	if _, err := os.Stat(scriptPath); err != nil {
		fmt.Fprintf(os.Stderr, "devstack-installer: %s not found: %v\n", scriptName, err)
		pauseIfInteractive(opts.Unattended)
		os.Exit(1)
	}

	// 3. Locate PowerShell
	psExe, err := findPowerShell()
	if err != nil {
		fmt.Fprintf(os.Stderr, "devstack-installer: %v\n", err)
		pauseIfInteractive(opts.Unattended)
		os.Exit(2)
	}

	// 4. Execute PowerShell installer script
	psArgs := buildPowerShellArgs(opts, scriptPath)
	cmd := exec.Command(psExe, psArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	// 5. Post-installation summary
	if exitCode == 0 {
		if opts.Uninstall {
			fmt.Println("\n======================================================================")
			fmt.Println("  devstack has been successfully uninstalled.")
			fmt.Println("======================================================================")
		} else if !opts.DryRun {
			fmt.Println("\n======================================================================")
			fmt.Println("  devstack for Windows has been successfully installed!")
			fmt.Println("======================================================================")
			fmt.Printf("  Installation directory: %s\n", opts.InstallDir)
			fmt.Printf("  Binaries on PATH:       %s\\bin\n", opts.InstallDir)
			fmt.Println("  Helper Windows Service: DevstackHelper (running)")
			fmt.Println("  Managed WSL Distro:     devstack (running)")
			fmt.Println("\n  To get started, open a new PowerShell or Terminal window and run:")
			fmt.Println("    devstack init")
			fmt.Println("    devstack doctor")
			fmt.Println("======================================================================")
		}
	} else if exitCode == 3010 {
		fmt.Println("\n======================================================================")
		fmt.Println("  A system restart is required to finish enabling Windows features.")
		fmt.Println("  Please reboot your computer, then re-run devstack-installer.")
		fmt.Println("======================================================================")
	} else {
		fmt.Fprintf(os.Stderr, "\ndevstack-installer: installation failed with exit code %d\n", exitCode)
	}

	pauseIfInteractive(opts.Unattended)
	os.Exit(exitCode)
}

func printBanner(uninstall bool) {
	fmt.Println("======================================================================")
	if uninstall {
		fmt.Println("  devstack - Windows & WSL2 Uninstaller")
	} else {
		fmt.Println("  devstack - Windows & WSL2 Setup Installer")
	}
	fmt.Println("======================================================================")
}

func printUsage() {
	fmt.Println(`Usage: devstack-installer.exe [options]

Options:
  --install-dir <path>  Target installation directory (default: C:\devstack)
  --rootfs <path>       Path to debian-12-wsl-rootfs.tar.gz archive
  --allow-reboot        Allow automatic reboot if Windows features require a restart
  --skip-wsl            Skip WSL feature enablement and distro provisioning
  --dry-run             Simulate installation without making system changes
  --upgrade             Upgrade existing devstack installation
  --uninstall           Uninstall devstack and remove system services
  --purge-distro        Unregister and remove the managed WSL distro (with --uninstall)
  --purge-data          Delete DPAPI secrets and remove installation directory (with --uninstall)
  -y, --unattended      Non-interactive unattended mode; do not prompt on completion
  --zip <path>          Path to external release ZIP archive
  -h, --help            Show this help message`)
}

func pauseIfInteractive(unattended bool) {
	if unattended {
		return
	}
	fmt.Print("\nPress Enter to exit...")
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
}
