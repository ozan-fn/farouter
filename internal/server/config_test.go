package server

import (
	"encoding/json"
	"os"
	"testing"
)

func writeConfigFile(t *testing.T, cfg Config) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write %s: %v", configPath, err)
	}
}

// A manual entry only carries a refreshToken; everything else must come back
// filled, with profileArn left for the refresh response.
func TestFillAccountDefaults(t *testing.T) {
	next := 3
	got, changed := fillAccountDefaults(AccountConfig{RefreshToken: "aorAAAA:sig"}, &next)

	if !changed {
		t.Fatal("bare entry must report a change")
	}
	if len(got.ID) != 36 {
		t.Errorf("id = %q, want a uuid", got.ID)
	}
	if got.Label != "Kiro 3" {
		t.Errorf("label = %q, want %q", got.Label, "Kiro 3")
	}
	if next != 4 {
		t.Errorf("next label = %d, want 4", next)
	}
	if got.AuthMethod != "social" {
		t.Errorf("authMethod = %q, want social", got.AuthMethod)
	}
	if len(got.MachineId) != 36 {
		t.Errorf("machineId = %q, want a uuid", got.MachineId)
	}
	if got.ProfileArn != "" {
		t.Errorf("profileArn = %q, want empty (filled after refresh)", got.ProfileArn)
	}
	if got.RefreshToken != "aorAAAA:sig" {
		t.Errorf("refreshToken changed: %q", got.RefreshToken)
	}
}

// A complete entry must be left exactly as written.
func TestFillAccountDefaults_CompleteUnchanged(t *testing.T) {
	full := AccountConfig{
		ID: "id-1", Label: "Kiro 9", RefreshToken: "aorAAAA:sig",
		ProfileArn: "arn:aws:codewhisperer:us-east-1:1:profile/X",
		AuthMethod: "builderid", MachineId: "machine-1",
	}
	next := 10
	got, changed := fillAccountDefaults(full, &next)

	if changed {
		t.Error("complete entry must not report a change")
	}
	if got != full {
		t.Errorf("entry mutated: %+v", got)
	}
	if next != 10 {
		t.Errorf("next label = %d, want 10", next)
	}
}

func TestNextKiroLabel(t *testing.T) {
	for _, tt := range []struct {
		name string
		accs []AccountConfig
		want int
	}{
		{"empty", nil, 1},
		{"sequential", []AccountConfig{{Label: "Kiro 1"}, {Label: "Kiro 2"}}, 3},
		{"gap", []AccountConfig{{Label: "Kiro 1"}, {Label: "Kiro 7"}}, 8},
		{"blank and odd labels", []AccountConfig{{Label: ""}, {Label: "Kiro 4"}, {Label: "custom"}}, 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextKiroLabel(tt.accs); got != tt.want {
				t.Errorf("nextKiroLabel = %d, want %d", got, tt.want)
			}
		})
	}
}

// Pasting an account into config.json while the server runs must survive the
// next saveConfig, which writes the in-memory pool over the file.
func TestMergeExternalAccounts_KeepsManualPaste(t *testing.T) {
	t.Chdir(t.TempDir())

	mem := AccountConfig{ID: "id-1", Label: "Kiro 1", RefreshToken: "aorA:1", AuthMethod: "social"}
	writeConfigFile(t, Config{Accounts: []AccountConfig{
		mem,
		{RefreshToken: "aorB:2"}, // pasted while the server was running
	}})

	got := mergeExternalAccounts([]AccountConfig{mem})

	if len(got) != 2 {
		t.Fatalf("accounts = %d, want 2 (manual entry kept)", len(got))
	}
	if got[1].RefreshToken != "aorB:2" {
		t.Errorf("kept token = %q, want aorB:2", got[1].RefreshToken)
	}
	if got[1].Label != "Kiro 2" || got[1].AuthMethod != "social" || len(got[1].ID) != 36 {
		t.Errorf("kept entry not completed: %+v", got[1])
	}
	if got[0] != mem {
		t.Errorf("known account mutated: %+v", got[0])
	}
}

// The same account under a different token spelling or id must not duplicate.
func TestMergeExternalAccounts_NoDuplicates(t *testing.T) {
	t.Chdir(t.TempDir())

	mem := AccountConfig{ID: "id-1", Label: "Kiro 1", RefreshToken: "aorA:1"}
	writeConfigFile(t, Config{Accounts: []AccountConfig{
		{ID: "id-1", Label: "Kiro 1", RefreshToken: "aorA:1"},
		{RefreshToken: "aorA:1"}, // same token, no id
	}})

	if got := mergeExternalAccounts([]AccountConfig{mem}); len(got) != 1 {
		t.Fatalf("accounts = %d, want 1 (no duplicates)", len(got))
	}
}

// No config file on disk (or an unreadable one) must not break saving.
func TestMergeExternalAccounts_MissingFile(t *testing.T) {
	t.Chdir(t.TempDir())

	mem := AccountConfig{ID: "id-1", Label: "Kiro 1", RefreshToken: "aorA:1"}
	got := mergeExternalAccounts([]AccountConfig{mem})
	if len(got) != 1 || got[0] != mem {
		t.Errorf("accounts = %+v, want the in-memory pool unchanged", got)
	}
}
