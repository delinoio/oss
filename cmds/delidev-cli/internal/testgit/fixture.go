// SPDX-License-Identifier: Apache-2.0
// Package testgit provides an isolated transport executable for integration
// fixtures. It never changes the production credential-free URL policy.
package testgit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func Executable(t testing.TB, sources map[string]string) string {
	binary, _ := Setup(t, sources)
	return binary
}

func Setup(t testing.TB, sources map[string]string) (string, func(string, string)) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mapping := filepath.Join(root, "sources.json")
	raw, err := json.Marshal(sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapping, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// The source map is read for each invocation so a fixture can add repositories
	// after an ordinary Worker starts, without changing its transport executable.
	source := fmt.Sprintf(`package main
import("encoding/json";"os";"os/exec")
func main(){
 realGit:=%q; mapping:=%q
 args:=os.Args[1:]; index:=-1
 for i,arg:=range args {if arg=="clone" {index=i;break}}
 original:="";remote:="origin"
 if index>=0 {
  raw,err:=os.ReadFile(mapping);if err!=nil{os.Exit(91)}
  sources:=map[string]string{};if json.Unmarshal(raw,&sources)!=nil{os.Exit(92)}
  original=args[len(args)-2]
  if source,ok:=sources[original];ok {
   args[len(args)-2]=source
   args=append(args[:index+1],append([]string{"--no-local","--no-hardlinks"},args[index+1:]...)...)
   // Fixture transport is a full independent copy with no object hard links.
   args=append([]string{"-c","protocol.file.allow=always"},args...)
  } else {os.Stderr.WriteString("Unmapped fixture Git source");os.Exit(93)}
  for _,arg:=range args {if len(arg)>9 && arg[:9]=="--origin=" {remote=arg[9:]}}
 }
 command:=exec.Command(realGit,args...);command.Env=os.Environ();command.Stdin=os.Stdin;command.Stdout=os.Stdout;command.Stderr=os.Stderr
 if err:=command.Run();err!=nil {if exit,ok:=err.(*exec.ExitError);ok{os.Exit(exit.ExitCode())};os.Exit(94)}
 if original!="" {
  target:=args[len(args)-1]
  command=exec.Command(realGit,"-C",target,"remote","set-url",remote,original)
  if command.Run()!=nil {os.Exit(95)}
 }
}
`, realGit, mapping)
	input := filepath.Join(root, "main.go")
	if err := os.WriteFile(input, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "git")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, input).CombinedOutput(); err != nil {
		t.Fatalf("Git fixture build: %v %s", err, out)
	}
	return binary, func(url, path string) {
		t.Helper()
		sources[url] = path
		raw, err := json.Marshal(sources)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mapping, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
