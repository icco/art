package models

import "testing"

func TestKindValidity(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"AccountPersonal", AccountPersonal.Valid()},
		{"AccountWork", AccountWork.Valid()},
		{"AccountInvalid", AccountKind("none").Valid()},
		{"SlotWork", SlotWork.Valid()},
		{"SlotPersonal", SlotPersonal.Valid()},
		{"SlotInvalid", SlotKind("x").Valid()},
		{"SourceProject", SourceProject.Valid()},
		{"SourceHabit", SourceHabit.Valid()},
		{"SourceInvalid", SourceKind("x").Valid()},
		{"AgentRunPlanner", AgentRunPlanner.Valid()},
		{"AgentRunTriage", AgentRunTriage.Valid()},
		{"AgentRunInvalid", AgentRunKind("x").Valid()},
		{"EmailArchive", EmailArchive.Valid()},
		{"EmailReply", EmailReply.Valid()},
		{"EmailKeep", EmailKeep.Valid()},
		{"EmailInvalid", EmailCategory("x").Valid()},
	}
	for _, c := range cases {
		expect := c.name[len(c.name)-7:] != "Invalid"
		if c.ok != expect {
			t.Errorf("%s: got %v want %v", c.name, c.ok, expect)
		}
	}
}

func TestBeforeCreateAssignsUUID(t *testing.T) {
	b := &Base{}
	if err := b.BeforeCreate(nil); err != nil {
		t.Fatal(err)
	}
	if b.ID == "" {
		t.Fatal("expected UUID to be assigned")
	}
	// Already-set ID should not be replaced.
	b.ID = "preset"
	_ = b.BeforeCreate(nil)
	if b.ID != "preset" {
		t.Fatalf("preset id overwritten: %q", b.ID)
	}
}

func TestAll(t *testing.T) {
	got := All()
	if len(got) < 5 {
		t.Fatalf("All() returned %d models, expected >=5", len(got))
	}
}
