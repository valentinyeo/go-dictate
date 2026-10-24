package notify

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBalloonScriptHasNoCallerText(t *testing.T) {
	title := "Mic\u2019; Write-Output GDMARKTITLE #"
	message := "Head\u2019set\u2018 GDMARKTEXT \u201a\u201b'x\nline2"
	cmd := balloonCommand(title, message, 3*time.Second)
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "GDMARKTITLE") || strings.Contains(args, "GDMARKTEXT") || strings.ContainsRune(args, '\u2019') {
		t.Error("arguments contain caller text")
	}

	flatMessage := strings.NewReplacer("\n", " ", "\r", " ").Replace(message)
	for _, want := range []string{"GD_BALLOON_TITLE=" + title, "GD_BALLOON_TEXT=" + flatMessage} {
		found := false
		for _, value := range cmd.Env {
			if value == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("environment missing %q", want)
		}
	}
}

func TestBalloonEnvReachesPowerShell(t *testing.T) {
	title := "Mic\u2019; Write-Output GDMARKTITLE #"
	message := "Head\u2019set\u2018 GDMARKTEXT \u201a\u201b'x\nline2"
	cmd := balloonCommand(title, message, 3*time.Second)
	child := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[Text.Encoding]::UTF8; [Console]::Out.Write($env:GD_BALLOON_TITLE + '|' + $env:GD_BALLOON_TEXT)")
	child.Env = cmd.Env
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell failed: %v: %s", err, output)
	}

	got := strings.TrimRight(strings.TrimPrefix(string(output), "\ufeff"), "\r\n")
	flatMessage := strings.NewReplacer("\n", " ", "\r", " ").Replace(message)
	if want := title + "|" + flatMessage; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestBalloonScriptParsesAsFixedStatements(t *testing.T) {
	statementCount := func(title, message string) string {
		t.Helper()
		cmd := balloonCommand(title, message, 3*time.Second)
		child := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `$tokens=$null; $e=$null; $ast=[System.Management.Automation.Language.Parser]::ParseInput($env:GD_SCRIPT,[ref]$tokens,[ref]$e); [Console]::Out.Write("$($e.Count) $($ast.EndBlock.Statements.Count)")`)
		child.Env = append(os.Environ(), "GD_SCRIPT="+cmd.Args[len(cmd.Args)-1])
		output, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("PowerShell failed: %v: %s", err, output)
		}
		fields := strings.Fields(strings.TrimPrefix(string(output), "\ufeff"))
		if len(fields) != 2 || fields[0] != "0" {
			t.Fatalf("unexpected parser result: %s", output)
		}
		return fields[1]
	}

	want := statementCount("A", "B")
	got := statementCount("Mic\u2019; Write-Output GDMARKTITLE #", "Head\u2019set\u2018 GDMARKTEXT \u201a\u201b'x\nline2")
	if got != want {
		t.Errorf("statement count = %s, want %s", got, want)
	}
}
