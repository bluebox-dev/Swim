package agent

import "testing"

func TestInventorySignatureIgnoresDrift(t *testing.T) {
	a := []byte(`{"hostname":"PC1","collected_at":"1","uptime_s":10,"memory":{"total":8,"free":3},"disks":[{"drive":"C:","free":42949672960}]}`)
	b := []byte(`{"hostname":"PC1","collected_at":"2","uptime_s":99,"memory":{"total":8,"free":5},"disks":[{"drive":"C:","free":42949672961}]}`)
	c := []byte(`{"hostname":"PC2","collected_at":"2","uptime_s":99,"memory":{"total":8,"free":5},"disks":[{"drive":"C:","free":42949672961}]}`)
	if inventorySignature(a) != inventorySignature(b) {
		t.Fatal("drifting values changed the signature")
	}
	if inventorySignature(a) == inventorySignature(c) {
		t.Fatal("a real change was not detected")
	}
}
