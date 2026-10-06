package agenttools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsolatedGoTestsSupportLocalGitFixtures(t *testing.T) {
	root := t.TempDir()
	source := `package fixture
import ("os"; "os/exec"; "path/filepath"; "strings"; "testing")
func TestFixture(t *testing.T) {
	for _,git:=range []string{"git"} {
  dir:=t.TempDir()
  for _,args:=range [][]string{{"init","-q"},{"add","file.txt"},{"-c","user.name=Fixture","-c","user.email=fixture@example.test","commit","-qm","fixture"},{"status","--porcelain"},{"clone","--bare",".","clone.git"},{"--git-dir","clone.git","worktree","add","-b","fixture-copy","../worktree"}} {
   if args[0]=="add" {if err:=os.WriteFile(filepath.Join(dir,"file.txt"),[]byte("local fixture"),0600);err!=nil {t.Fatal(err)}}
   cmd:=exec.Command(git,args...);cmd.Dir=dir
   output,err:=cmd.CombinedOutput();if err!=nil {t.Fatalf("git fixture %s %v: %s %v",git,args,output,err)}
   if args[0]=="status" && strings.TrimSpace(string(output))!="" {t.Fatalf("unclean fixture: %s",output)}
  }
 }
}
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/fixture\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "go.mod", "fixture_test.go"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prepare fixture %v: %s %v", args, output, err)
		}
	}
	for _, binary := range []string{"", "/usr/bin/git"} {
		t.Run(binary, func(t *testing.T) {
			result, err := (Host{Timeout: 2 * time.Minute, GitBinary: binary}).execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}}, call("run_go_tests", `{"package":"./..."}`))
			if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "ok") || result.PermissionDenial {
				t.Fatalf("isolated Git fixtures: %+v %v", result, err)
			}
		})
	}
}
