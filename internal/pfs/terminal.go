package pfs

import (
	"os"
	"os/exec"
	"runtime"
)

func clearScreen() {
	command := "clear"
	if runtime.GOOS == "windows" {
		command = "cmd"
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(command, "/c", "cls")
	} else {
		cmd = exec.Command(command)
	}
	cmd.Stdout = os.Stdout
	_ = cmd.Run()
}
