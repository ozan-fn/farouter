package rtk

import (
	"strings"
	"testing"
)

func TestGitStatusParser(t *testing.T) {
	input := `On branch main
Your branch is up to date with 'origin/main'.

nothing to commit, working tree clean`

	expected := "* main...origin/main\nclean — nothing to commit"
	
	p := &GitStatusParser{}
	if !p.Match(input) {
		t.Fatal("GitStatusParser should match git status output")
	}
	
	result := p.Parse(input)
	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

func TestGitStatusWithChanges(t *testing.T) {
	input := `On branch main
Your branch is up to date with 'origin/main'.

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   main.go
	modified:   internal/rtk/rtk.go

no changes added to commit (use "git add" and/or "git commit -a")`

	p := &GitStatusParser{}
	result := p.Parse(input)
	
	if !strings.Contains(result, "main...origin/main") {
		t.Errorf("Expected branch info, got: %s", result)
	}
	if !strings.Contains(result, "modified:   main.go") {
		t.Errorf("Expected modified files, got: %s", result)
	}
}

func TestGitLogParser(t *testing.T) {
	input := `commit 5e08f2eca3a2a75bb2dd30081b074d4a2b31af8a
Author: ozan-fn <me@ozan.my.id>
Date:   Sun Jul 26 16:02:03 2026 +0700

    chore(build): add Makefile for build and dev commands

commit 7fb3c39db2a3fb2287eca08b713b296c4ce80bee
Author: ozan-fn <me@ozan.my.id>
Date:   Sun Jul 26 15:53:39 2026 +0700

    fix(config): preserve password field on saveConfig
    
    saveConfig() was creating empty Config{} without password field,
    causing password to disappear from config.json on every save.
    Now initializes with Password: cfgPassword from global state.`

	p := &GitLogParser{}
	if !p.Match(input) {
		t.Fatal("GitLogParser should match git log output")
	}
	
	result := p.Parse(input)
	
	if !strings.Contains(result, "5e08f2e") {
		t.Errorf("Expected short hash, got: %s", result)
	}
	if !strings.Contains(result, "chore(build)") {
		t.Errorf("Expected commit subject, got: %s", result)
	}
	if !strings.Contains(result, "<ozan-fn>") {
		t.Errorf("Expected author, got: %s", result)
	}
	if !strings.Contains(result, "[+3 lines omitted]") {
		t.Errorf("Expected omitted body indicator, got: %s", result)
	}
}

func TestLsParser(t *testing.T) {
	input := `total 26188
drwxr-xr-x  7 ozan ozan     4096 Jul 26 16:08 .
drwxr-xr-x 14 ozan ozan     4096 Jul 25 23:02 ..
drwxr-xr-x 16 ozan ozan     4096 Jul 24 14:02 9router
-rw-r--r--  1 ozan ozan     6830 Jul 26 12:11 README.md
-rw-r--r--  1 ozan ozan     2964 Jul 23 23:39 decrypt.go
drwxr-xr-x  6 ozan ozan     4096 Jul 26 16:01 farouter`

	p := &LsParser{}
	if !p.Match(input) {
		t.Fatal("LsParser should match ls -la output")
	}
	
	result := p.Parse(input)
	
	if !strings.Contains(result, "755  9router/") {
		t.Errorf("Expected directory with octal perms, got: %s", result)
	}
	if !strings.Contains(result, "644  README.md") {
		t.Errorf("Expected file with size, got: %s", result)
	}
	// Should not contain . or ..
	if strings.Contains(result, "..") {
		t.Errorf("Should not contain parent directory, got: %s", result)
	}
}

func TestGoTestParser(t *testing.T) {
	input := `=== RUN   TestGitStatusParser
--- PASS: TestGitStatusParser (0.00s)
=== RUN   TestLsParser
--- PASS: TestLsParser (0.00s)
PASS
ok  	farouter/internal/rtk	0.123s`

	p := &GoTestParser{}
	if !p.Match(input) {
		t.Fatal("GoTestParser should match go test output")
	}
	
	result := p.Parse(input)
	
	if !strings.Contains(result, "ok") {
		t.Errorf("Expected ok status, got: %s", result)
	}
}

func TestGoTestWithFailures(t *testing.T) {
	input := `=== RUN   TestFailing
--- FAIL: TestFailing (0.00s)
    rtk_test.go:123: assertion failed
FAIL
FAIL	farouter/internal/rtk	0.123s`

	p := &GoTestParser{}
	result := p.Parse(input)
	
	if !strings.Contains(result, "FAIL") {
		t.Errorf("Expected FAIL status, got: %s", result)
	}
	if !strings.Contains(result, "assertion failed") {
		t.Errorf("Expected failure details, got: %s", result)
	}
}

func TestDockerPsParser(t *testing.T) {
	input := `CONTAINER ID   IMAGE          COMMAND                  CREATED        STATUS        PORTS                    NAMES
abc123456789   nginx:latest   "/docker-entrypoint.…"   2 hours ago    Up 2 hours    0.0.0.0:80->80/tcp       web
def987654321   redis:7        "docker-entrypoint.s…"   2 hours ago    Up 2 hours    6379/tcp                 cache`

	p := &DockerPsParser{}
	if !p.Match(input) {
		t.Fatal("DockerPsParser should match docker ps output")
	}
	
	result := p.Parse(input)
	
	if !strings.Contains(result, "abc123456789") {
		t.Errorf("Expected container ID, got: %s", result)
	}
	if !strings.Contains(result, "nginx:latest") {
		t.Errorf("Expected image name, got: %s", result)
	}
	if !strings.Contains(result, "Up 2 hours") {
		t.Errorf("Expected status, got: %s", result)
	}
}

func TestGenericParser(t *testing.T) {
	input := strings.Repeat("line\n", 100)
	
	p := &GenericParser{maxLines: 20}
	result := p.Parse(input)
	
	lines := strings.Split(result, "\n")
	if len(lines) > 22 { // 20 lines + omitted message + blank
		t.Errorf("Expected max 22 lines, got %d", len(lines))
	}
	
	if !strings.Contains(result, "[... 80 lines omitted]") {
		t.Errorf("Expected omitted message, got: %s", result)
	}
}

func TestProcessOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "git status",
			input:    "On branch main\nYour branch is up to date with 'origin/main'.\n\nnothing to commit, working tree clean",
			contains: "clean — nothing to commit",
		},
		{
			name:     "ls output",
			input:    "total 100\ndrwxr-xr-x 2 user user 4096 Jul 26 10:00 test",
			contains: "755  test/",
		},
		{
			name:     "go test",
			input:    "PASS\nok  \tpackage\t0.123s",
			contains: "ok",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessOutput(tt.input)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("Expected result to contain %q, got: %s", tt.contains, result)
			}
		})
	}
}

func TestProcessToolMessages(t *testing.T) {
	messages := []map[string]any{
		{
			"role":    "user",
			"content": "Run git status",
		},
		{
			"role": "tool",
			"content": `**Tool:** bash
**Input:** { "command": "git status" }
**Output:**
On branch main
Your branch is up to date with 'origin/main'.

nothing to commit, working tree clean`,
		},
	}
	
	result := ProcessToolMessages(messages)
	
	// User message should NOT be processed
	userMsg := result[0]
	userContent, _ := userMsg["content"].(string)
	if userContent != "Run git status" {
		t.Errorf("User message should not be RTK-processed, got: %s", userContent)
	}
	
	// Tool message should be filtered
	toolMsg := result[1]
	content, _ := toolMsg["content"].(string)
	
	if !strings.Contains(content, "clean — nothing to commit") {
		t.Errorf("Expected filtered output, got: %s", content)
	}
	if strings.Contains(content, "Your branch is up to date") {
		t.Errorf("Expected compact output without verbose text, got: %s", content)
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1024", "1K"},
		{"1048576", "1M"},
		{"1073741824", "1G"},
		{"512", "512"},
		{"1500", "1K"},
	}
	
	for _, tt := range tests {
		result := humanSize(tt.input)
		if result != tt.expected {
			t.Errorf("humanSize(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestPermsToOctal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"drwxr-xr-x", "755"},
		{"-rw-r--r--", "644"},
		{"-rwxrwxrwx", "777"},
		{"-rw-------", "600"},
	}
	
	for _, tt := range tests {
		result := permsToOctal(tt.input)
		if result != tt.expected {
			t.Errorf("permsToOctal(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
