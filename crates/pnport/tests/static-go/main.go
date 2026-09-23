//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	const path = "node_modules/dep/file.txt"
	fd, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err != nil {
		os.Exit(21)
	}
	defer syscall.Close(fd)
	data := make([]byte, 13)
	if n, err := syscall.Read(fd, data); err != nil || n != len(data) || string(data) != "package bytes" {
		os.Exit(22)
	}
	mapped, err := syscall.Mmap(fd, 0, 13, syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil || string(mapped) != "package bytes" {
		os.Exit(23)
	}
	_ = syscall.Munmap(mapped)
	dir, err := syscall.Open("node_modules/dep", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		os.Exit(24)
	}
	defer syscall.Close(dir)
	other, err := syscall.Openat(dir, "file.txt", syscall.O_RDONLY, 0)
	if err != nil {
		os.Exit(25)
	}
	_ = syscall.Close(other)
	if _, err := os.ReadDir("node_modules/dep"); err != nil {
		os.Exit(26)
	}
	if err := os.WriteFile(path, []byte("bad"), 0600); !errors.Is(err, syscall.EROFS) {
		os.Exit(27)
	}
	if err := os.WriteFile("output-go.txt", []byte("go"), 0600); err != nil {
		os.Exit(28)
	}
	if len(os.Args) == 1 {
		child := exec.Command(os.Args[0], "child")
		child.Env = []string{}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Run(); err != nil {
			os.Exit(29)
		}
	}
	fmt.Println("static-go-ok")
}
