package kernel

import "testing"

func TestRoleFQRNAndActorFQNRemainDistinctIdentities(t *testing.T) {
	fqrn, err := ParseRoleFQRN("architect")
	if err != nil || fqrn != RoleFQRN("architect") {
		t.Fatalf("fqrn=%q err=%v", fqrn, err)
	}
	actor, err := ParseActorFQN("teams::architect-1")
	if err != nil {
		t.Fatal(err)
	}
	derived, err := RoleFQRNFromActor(actor)
	if err != nil || derived != fqrn {
		t.Fatalf("derived=%q err=%v", derived, err)
	}
	if _, err := ParseRoleFQRN(string(actor)); err == nil {
		t.Fatal("actor FQN was accepted as a role FQRN")
	}
	if _, err := ParseActorFQN(string(fqrn)); err == nil {
		t.Fatal("role FQRN was accepted as an actor FQN")
	}
	if _, err := ParseRoleFQRN("senior-coder"); err != nil {
		t.Fatalf("hyphenated role FQRN rejected: %v", err)
	}
	numberedRole, err := RoleFQRNFromActor("teams::coder-1-2")
	if err != nil || numberedRole != "coder-1" {
		t.Fatalf("numbered role fqrn=%q err=%v", numberedRole, err)
	}
}
