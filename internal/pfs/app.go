package pfs

import (
	"bufio"
	"flag"
	"fmt"
	"os"
)

func Run(args []string) int {
	if len(args) < 1 {
		return runShell(nil)
	}
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] == '-' {
		return runShell(args)
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "run":
		return runShell(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Println(`File System - portable virtual file system

Usage:
  pfs init [-disk fms.pfs] [-force]
  pfs run  [-disk fms.pfs]
  pfs      (defaults to run)

Shell commands:
  main/help, useradd, su, pwd, clear/cls, mkdir, cd, touch, vim/write,
  more, cp, mv, rename, tree, ls, ll, stat, detail, rm, exit`)
}

func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	diskPath := fs.String("disk", defaultDiskPath(), "virtual disk file")
	force := fs.Bool("force", false, "overwrite an existing disk")
	_ = fs.Parse(args)

	if _, err := os.Stat(*diskPath); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "%s already exists; use -force to overwrite it\n", *diskPath)
		return 1
	}

	if err := saveDisk(*diskPath, newDisk()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("initialized %s\n", *diskPath)
	return 0
}

func runShell(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	diskPath := fs.String("disk", defaultDiskPath(), "virtual disk file")
	_ = fs.Parse(args)

	if _, err := os.Stat(*diskPath); os.IsNotExist(err) {
		if err := saveDisk(*diskPath, newDisk()); err != nil {
			fmt.Fprintf(os.Stderr, "failed to initialize disk: %v\n", err)
			return 1
		}
		fmt.Printf("disk not found, initialized %s\n", *diskPath)
	}

	disk, err := loadDisk(*diskPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load disk: %v\n", err)
		return 1
	}

	session := &Session{
		disk:      disk,
		diskPath:  *diskPath,
		currentID: disk.RootID,
		reader:    bufio.NewReader(os.Stdin),
		out:       os.Stdout,
	}
	if err := session.login(""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	session.loop()
	return 0
}

func defaultDiskPath() string {
	return "fms.pfs"
}
